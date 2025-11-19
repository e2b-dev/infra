package nbd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd/testutils"
)

func TestPathDirect4MBWrite(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("the nbd requires root privileges to run")
	}

	// Create a device that's at least 4MB (use 10MB to be safe)
	size := int64(10 * 1024 * 1024)
	blockSize := int64(4096)

	// Create zero device
	emptyDevice, err := testutils.NewZeroDevice(size, blockSize)
	if err != nil {
		t.Fatalf("failed to create zero device: %v", err)
	}

	// Create cache path
	cowCachePath := filepath.Join(os.TempDir(), fmt.Sprintf("test-rootfs.ext4.cow.cache-%s", uuid.New().String()))
	t.Cleanup(func() {
		os.RemoveAll(cowCachePath)
	})

	// Create cache
	cache, err := block.NewCache(
		size,
		blockSize,
		cowCachePath,
		false,
	)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	// Create overlay
	overlay := block.NewOverlay(emptyDevice, cache)
	t.Cleanup(func() {
		overlay.Close()
	})

	// Get NBD device
	nbdContext := context.Background()
	devicePath, deviceCleanup, err := testutils.GetNBDDevice(nbdContext, overlay)
	t.Cleanup(func() {
		deviceCleanup.Run(t.Context(), 30*time.Second)
	})
	if err != nil {
		t.Fatalf("failed to get nbd device: %v", err)
	}

	t.Logf("NBD device path: %s", devicePath)

	// We need to ensure buffer is page aligned to be able to use O_DIRECT.
	const bs = 4 * 1024 * 1024
	buf, err := unix.Mmap(-1, 0, bs, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANON)
	if err != nil {
		panic(err)
	}

	t.Cleanup(func() {
		unix.Munmap(buf)
	})

	// Open device with direct I/O to trigger unbuffered write of 4MB.
	deviceFile, err := os.OpenFile(string(devicePath), unix.O_DIRECT|unix.O_RDWR, 0)
	if err != nil {
		t.Fatalf("failed to open device: %v", err)
	}
	t.Cleanup(func() {
		deviceFile.Close()
	})

	// Write 4MB at offset 0
	n, err := deviceFile.WriteAt(buf, 0)
	if err != nil {
		t.Fatalf("failed to write to device: %v", err)
	}
	if n != len(buf) {
		t.Fatalf("partial write: expected %d bytes, wrote %d bytes", len(buf), n)
	}

	// Verify the write by reading it back
	readData := make([]byte, bs)
	n, err = deviceFile.ReadAt(readData, 0)
	if err != nil {
		t.Fatalf("failed to read from device: %v", err)
	}
	if n != len(readData) {
		t.Fatalf("partial read: expected %d bytes, read %d bytes", len(readData), n)
	}

	if !bytes.Equal(buf, readData) {
		t.Fatalf("data mismatch: expected %v, got %v", buf, readData)
	}

	t.Logf("Successfully wrote and verified 4MB to NBD device")
}
