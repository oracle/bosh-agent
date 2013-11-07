package platform

import (
	testdisk "bosh/platform/disk/testhelpers"
	boshsettings "bosh/settings"
	testsys "bosh/system/testhelpers"
	"fmt"
	sigar "github.com/cloudfoundry/gosigar"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUbuntuSetupSsh(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	fakeFs.HomeDirHomeDir = "/some/home/dir"

	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)
	ubuntu.SetupSsh("some public key", "vcap")

	sshDirPath := "/some/home/dir/.ssh"
	sshDirStat := fakeFs.GetFileTestStat(sshDirPath)

	assert.Equal(t, fakeFs.HomeDirUsername, "vcap")

	assert.NotNil(t, sshDirStat)
	assert.Equal(t, sshDirStat.CreatedWith, "MkdirAll")
	assert.Equal(t, sshDirStat.FileMode, os.FileMode(0700))
	assert.Equal(t, sshDirStat.Username, "vcap")

	authKeysStat := fakeFs.GetFileTestStat(filepath.Join(sshDirPath, "authorized_keys"))

	assert.NotNil(t, authKeysStat)
	assert.Equal(t, authKeysStat.CreatedWith, "WriteToFile")
	assert.Equal(t, authKeysStat.FileMode, os.FileMode(0600))
	assert.Equal(t, authKeysStat.Username, "vcap")
	assert.Equal(t, authKeysStat.Content, "some public key")
}

func TestUbuntuSetupDhcp(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	testUbuntuSetupDhcp(t, fakeFs, fakeCmdRunner, fakePartitioner)

	assert.Equal(t, len(fakeCmdRunner.RunCommands), 2)
	assert.Equal(t, fakeCmdRunner.RunCommands[0], []string{"pkill", "dhclient3"})
	assert.Equal(t, fakeCmdRunner.RunCommands[1], []string{"/etc/init.d/networking", "restart"})
}

func TestUbuntuSetupDhcpWithPreExistingConfiguration(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	fakeFs.WriteToFile("/etc/dhcp3/dhclient.conf", EXPECTED_DHCP_CONFIG)
	testUbuntuSetupDhcp(t, fakeFs, fakeCmdRunner, fakePartitioner)

	assert.Equal(t, len(fakeCmdRunner.RunCommands), 0)
}

func testUbuntuSetupDhcp(t *testing.T, fakeFs *testsys.FakeFileSystem, fakeCmdRunner *testsys.FakeCmdRunner, fakePartitioner *testdisk.FakePartitioner) {
	networks := boshsettings.Networks{
		"bosh": boshsettings.NetworkSettings{
			Default: []string{"dns"},
			Dns:     []string{"xx.xx.xx.xx", "yy.yy.yy.yy", "zz.zz.zz.zz"},
		},
		"vip": boshsettings.NetworkSettings{
			Default: []string{},
			Dns:     []string{"aa.aa.aa.aa"},
		},
	}

	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)
	ubuntu.SetupDhcp(networks)

	dhcpConfig := fakeFs.GetFileTestStat("/etc/dhcp3/dhclient.conf")
	assert.NotNil(t, dhcpConfig)
	assert.Equal(t, dhcpConfig.Content, EXPECTED_DHCP_CONFIG)
}

const EXPECTED_DHCP_CONFIG = `# Generated by bosh-agent

option rfc3442-classless-static-routes code 121 = array of unsigned integer 8;

send host-name "<hostname>";

request subnet-mask, broadcast-address, time-offset, routers,
	domain-name, domain-name-servers, domain-search, host-name,
	netbios-name-servers, netbios-scope, interface-mtu,
	rfc3442-classless-static-routes, ntp-servers;

prepend domain-name-servers zz.zz.zz.zz;
prepend domain-name-servers yy.yy.yy.yy;
prepend domain-name-servers xx.xx.xx.xx;
`

func TestUbuntuSetupEphemeralDiskWithPath(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	fakeCmdRunner.CommandResults = map[string][]string{
		"sfdisk -s /dev/xvda": []string{fmt.Sprintf("%d", 1024*1024*1024), ""},
	}
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	fakeFs.WriteToFile("/dev/xvda", "")

	err := ubuntu.SetupEphemeralDiskWithPath("/dev/sda", "/data-dir")
	assert.NoError(t, err)

	dataDir := fakeFs.GetFileTestStat("/data-dir")
	assert.Equal(t, "MkdirAll", dataDir.CreatedWith)
	assert.Equal(t, os.FileMode(0750), dataDir.FileMode)

	assert.Equal(t, "/dev/xvda", fakePartitioner.PartitionDevicePath)
	assert.Equal(t, 2, len(fakePartitioner.PartitionPartitions))

	swapPartition := fakePartitioner.PartitionPartitions[0]
	ext4Partition := fakePartitioner.PartitionPartitions[1]

	assert.Equal(t, "swap", swapPartition.Type)
	assert.Equal(t, "linux", ext4Partition.Type)

	assert.Equal(t, 5, len(fakeCmdRunner.RunCommands))
	assert.Equal(t, []string{"sfdisk", "-s", "/dev/xvda"}, fakeCmdRunner.RunCommands[0])
	assert.Equal(t, []string{"mkswap", "/dev/xvda1"}, fakeCmdRunner.RunCommands[1])
	assert.Equal(t, []string{"mke2fs", "-t", "ext4", "-j", "/dev/xvda2"}, fakeCmdRunner.RunCommands[2])
	assert.Equal(t, []string{"swapon", "/dev/xvda1"}, fakeCmdRunner.RunCommands[3])
	assert.Equal(t, []string{"mount", "/dev/xvda2", "/data-dir"}, fakeCmdRunner.RunCommands[4])

	sysLogStats := fakeFs.GetFileTestStat("/data-dir/sys/log")
	assert.NotNil(t, sysLogStats)
	assert.Equal(t, "MkdirAll", sysLogStats.CreatedWith)
	assert.Equal(t, os.FileMode(0750), sysLogStats.FileMode)

	sysRunStats := fakeFs.GetFileTestStat("/data-dir/sys/run")
	assert.NotNil(t, sysRunStats)
	assert.Equal(t, "MkdirAll", sysRunStats.CreatedWith)
	assert.Equal(t, os.FileMode(0750), sysRunStats.FileMode)
}

func TestUbuntuGetRealDevicePathWithMultiplePossibleDevices(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	fakeFs.WriteToFile("/dev/xvda", "")
	fakeFs.WriteToFile("/dev/vda", "")

	realPath, err := ubuntu.getRealDevicePath("/dev/sda")
	assert.NoError(t, err)
	assert.Equal(t, "/dev/xvda", realPath)
}

func TestUbuntuGetRealDevicePathWithDelayWithinTimeout(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	time.AfterFunc(time.Second, func() {
		fakeFs.WriteToFile("/dev/xvda", "")
	})

	realPath, err := ubuntu.getRealDevicePath("/dev/sda")
	assert.NoError(t, err)
	assert.Equal(t, "/dev/xvda", realPath)
}

func TestUbuntuGetRealDevicePathWithDelayBeyondTimeout(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)
	ubuntu.diskWaitTimeout = time.Second

	time.AfterFunc(2*time.Second, func() {
		fakeFs.WriteToFile("/dev/xvda", "")
	})

	_, err := ubuntu.getRealDevicePath("/dev/sda")
	assert.Error(t, err)
}

func TestUbuntuCalculateEphemeralDiskPartitionSizesWhenDiskIsBiggerThanTwiceTheMemory(t *testing.T) {
	memStats := sigar.Mem{}
	memStats.Get()
	totalMemInKb := memStats.Total
	totalMemInBlocks := totalMemInKb * uint64(1024/512)

	diskSizeInBlocks := totalMemInBlocks*2 + 1
	expectedSwap := totalMemInBlocks
	testUbuntuCalculateEphemeralDiskPartitionSizes(t, diskSizeInBlocks, expectedSwap)
}

func TestUbuntuCalculateEphemeralDiskPartitionSizesWhenDiskTwiceTheMemoryOrSmaller(t *testing.T) {
	memStats := sigar.Mem{}
	memStats.Get()
	totalMemInKb := memStats.Total
	totalMemInBlocks := totalMemInKb * uint64(1024/512)

	diskSizeInBlocks := totalMemInBlocks*2 - 1
	expectedSwap := diskSizeInBlocks / 2
	testUbuntuCalculateEphemeralDiskPartitionSizes(t, diskSizeInBlocks, expectedSwap)
}

func testUbuntuCalculateEphemeralDiskPartitionSizes(t *testing.T, diskSizeInBlocks, expectedSwap uint64) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	fakeCmdRunner.CommandResults = map[string][]string{
		"sfdisk -s /dev/hda": []string{fmt.Sprintf("%d", diskSizeInBlocks), ""},
	}

	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	swapSize, linuxSize, err := ubuntu.calculateEphemeralDiskPartitionSizes("/dev/hda")

	assert.NoError(t, err)
	assert.Equal(t, expectedSwap, swapSize)
	assert.Equal(t, diskSizeInBlocks-expectedSwap, linuxSize)
}

func TestUbuntuGetCpuLoad(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	load, err := ubuntu.GetCpuLoad()
	assert.NoError(t, err)
	assert.True(t, load.One > 0)
	assert.True(t, load.Five > 0)
	assert.True(t, load.Fifteen > 0)
}

func TestUbuntuGetCpuStats(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	stats, err := ubuntu.GetCpuStats()
	assert.NoError(t, err)
	assert.True(t, stats.User > 0)
	assert.True(t, stats.Sys > 0)
	assert.True(t, stats.Total > 0)
}

func TestUbuntuGetMemStats(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	stats, err := ubuntu.GetMemStats()
	assert.NoError(t, err)
	assert.True(t, stats.Total > 0)
	assert.True(t, stats.Used > 0)
}

func TestUbuntuGetSwapStats(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	stats, err := ubuntu.GetSwapStats()
	assert.NoError(t, err)
	assert.True(t, stats.Total > 0)
}

func TestUbuntuGetDiskStats(t *testing.T) {
	fakeFs, fakeCmdRunner, fakePartitioner := getUbuntuDependencies()
	ubuntu := newUbuntuPlatform(fakeFs, fakeCmdRunner, fakePartitioner)

	stats, err := ubuntu.GetDiskStats("/")
	assert.NoError(t, err)
	assert.True(t, stats.Total > 0)
	assert.True(t, stats.Used > 0)
	assert.True(t, stats.InodeTotal > 0)
	assert.True(t, stats.InodeUsed > 0)
}

func getUbuntuDependencies() (fs *testsys.FakeFileSystem, cmdRunner *testsys.FakeCmdRunner, diskPartitioner *testdisk.FakePartitioner) {
	fs = &testsys.FakeFileSystem{}
	cmdRunner = &testsys.FakeCmdRunner{}
	diskPartitioner = &testdisk.FakePartitioner{}
	return
}
