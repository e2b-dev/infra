package nbd_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd/testutils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestPathDirect_Direct4MBWrite(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)
	size := int64(10 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, unix.O_DIRECT|unix.O_RDWR)

	const bs = 4 * 1024 * 1024
	buf, err := unix.Mmap(-1, 0, bs, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANON)
	if err != nil {
		panic(err)
	}

	t.Cleanup(func() {
		unix.Munmap(buf)
	})

	n, err := deviceFile.WriteAt(buf, 0)
	require.NoError(t, err, "failed to write to device")
	require.Equal(t, len(buf), n, "partial write")

	readData := make([]byte, bs)
	n, err = deviceFile.ReadAt(readData, 0)
	require.NoError(t, err, "failed to read from device")
	require.Equal(t, len(readData), n, "partial read")
	require.Equal(t, buf, readData, "data mismatch")
}

// We usually see the 32MB write be split into smaller writes, even on O_DIRECT.
func TestPathDirect_Direct32MBWrite(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)
	size := int64(256 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, unix.O_DIRECT|unix.O_RDWR)

	const bs = 32 * 1024 * 1024
	buf, err := unix.Mmap(-1, 0, bs, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANON)
	require.NoError(t, err, "failed to mmap")

	t.Cleanup(func() {
		unix.Munmap(buf)
	})

	n, err := deviceFile.WriteAt(buf, 0)
	require.NoError(t, err, "failed to write to device")
	require.Equal(t, len(buf), n, "partial write")

	readData := make([]byte, bs)
	n, err = deviceFile.ReadAt(readData, 0)
	require.NoError(t, err, "failed to read from device")
	require.Equal(t, len(readData), n, "partial read")
	require.Equal(t, buf, readData, "data mismatch")
}

func TestPathDirect_Write(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	size := int64(5 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, os.O_RDWR)

	const writeSize = 1024 * 1024
	testData := make([]byte, writeSize)
	_, err = rand.Read(testData)
	require.NoError(t, err, "failed to generate random data")

	n, err := deviceFile.WriteAt(testData, 0)
	require.NoError(t, err, "failed to write data to device")
	require.Equal(t, len(testData), n, "partial write")

	readData := make([]byte, writeSize)
	n, err = deviceFile.ReadAt(readData, 0)
	require.NoError(t, err, "failed to read data from device")
	require.Equal(t, len(readData), n, "partial read")
	require.Equal(t, testData, readData, "data mismatch")
}

func TestPathDirect_WriteAtOffset(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)
	size := int64(5 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, os.O_RDWR)

	const writeSize = 512 * 1024
	const writeOffset = 512 * 1024
	testData := make([]byte, writeSize)
	_, err = rand.Read(testData)
	require.NoError(t, err, "failed to generate random data")

	n, err := deviceFile.WriteAt(testData, writeOffset)
	require.NoError(t, err, "failed to write data to device")
	require.Equal(t, len(testData), n, "partial write")

	readData := make([]byte, writeSize)
	n, err = deviceFile.ReadAt(readData, writeOffset)
	require.NoError(t, err, "failed to read data from device")
	require.Equal(t, len(readData), n, "partial read")
	require.Equal(t, testData, readData, "data mismatch")
}

func TestPathDirect_LargeWrite(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	size := int64(1200 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, os.O_RDWR)

	time.Sleep(1 * time.Second)
	cmd := exec.CommandContext(t.Context(), "dd", "if=/dev/zero", "of="+deviceFile.Name(), "bs=1G", "count=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	require.NoError(t, err, "failed to execute dd command")
}

func TestPathLargeRead(t *testing.T) {
	t.Parallel()

	featureFlags, err := featureflags.NewClient()
	require.NoError(t, err)

	size := int64(1200 * 1024 * 1024)

	deviceFile := setupNBDDevice(t, featureFlags, size, header.RootfsBlockSize, os.O_RDONLY)
	time.Sleep(1 * time.Second)

	cmd := exec.CommandContext(t.Context(), "dd", "if="+deviceFile.Name(), "of=/dev/null", "bs=1G", "count=1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	require.NoError(t, err, "failed to execute dd command")
}

func setupNBDDevice(t *testing.T, featureFlags *featureflags.Client, size, blockSize int64, flags int) *os.File {
	t.Helper()

	require.Equal(t, 0, os.Geteuid(), "the nbd requires root privileges to run")

	emptyDevice, err := testutils.NewZeroDevice(size, blockSize)
	require.NoError(t, err, "failed to create zero device")

	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("test-rootfs.ext4.cow.cache-%s", uuid.New().String()))
	t.Cleanup(func() {
		os.RemoveAll(cowCachePath)
	})

	cache, err := block.NewCache(
		size,
		blockSize,
		cowCachePath,
		false,
	)
	require.NoError(t, err, "failed to create cache")

	overlay := block.NewOverlay(emptyDevice, cache)
	t.Cleanup(func() {
		overlay.Close()
	})

	nbdContext := context.Background()
	devicePath, deviceCleanup, err := testutils.GetNBDDevice(nbdContext, overlay, featureFlags)
	t.Cleanup(func() {
		deviceCleanup.Run(t.Context(), 30*time.Second)
	})
	require.NoError(t, err, "failed to get nbd device")

	t.Logf("NBD device path: %s", devicePath)

	deviceFile, err := os.OpenFile(devicePath, flags, 0)
	require.NoError(t, err, "failed to open device")
	t.Cleanup(func() {
		deviceFile.Close()
	})

	return deviceFile
}
