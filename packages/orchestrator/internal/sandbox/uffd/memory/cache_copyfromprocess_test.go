package memory

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestCopyFromProcess_HelperProcess is a helper process that allocates memory
// with known content and waits for the test to read it.
func TestCopyFromProcess_HelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_COPY_HELPER_PROCESS") != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	// Allocate memory with known content
	sizeStr := os.Getenv("GO_TEST_MEMORY_SIZE")
	size, err := strconv.ParseUint(sizeStr, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse memory size: %v\n", err)
		os.Exit(1)
	}

	// Allocate memory using mmap (similar to testutils.NewPageMmap but simpler)
	mem, err := unix.Mmap(-1, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to mmap memory: %v\n", err)
		os.Exit(1)
	}
	defer unix.Munmap(mem)

	// Fill memory with a pattern: each byte is its offset modulo 256
	for i := range mem {
		mem[i] = byte(i % 256)
	}

	// Write the memory address to stdout (as 8 bytes, little endian)
	addr := uint64(0)
	if len(mem) > 0 {
		addr = uint64(uintptr(unsafe.Pointer(&mem[0])))
	}
	err = binary.Write(os.Stdout, binary.LittleEndian, addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to write address: %v\n", err)
		os.Exit(1)
	}

	// Signal ready by closing stdout
	os.Stdout.Close()

	// Wait for SIGTERM to exit
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	select {
	case <-sigChan:
		// Exit cleanly
	case <-time.After(30 * time.Second):
		// Timeout after 30 seconds
		fmt.Fprintf(os.Stderr, "helper process timeout\n")
		os.Exit(1)
	}
}

func TestCopyFromProcess_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := int64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(
		size,
		header.PageSize,
		tmpFile,
		false,
	)
	require.NoError(t, err)
	defer cache.Close()

	// Start helper process
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", size))
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})

	// Read the memory address from the helper process
	var addr uint64
	err = binary.Read(stdout, binary.LittleEndian, &addr)
	require.NoError(t, err)
	stdout.Close()

	// Wait a bit for the process to be ready
	time.Sleep(100 * time.Millisecond)

	// Test copying a single range
	ranges := []block.Range{
		{Start: int64(addr), Size: size},
	}

	err = cache.CopyFromProcess(ctx, cmd.Process.Pid, ranges)
	require.NoError(t, err)

	// Verify the copied data
	data := make([]byte, size)
	n, err := cache.ReadAt(data, 0)
	require.NoError(t, err)
	require.Equal(t, int(size), n)

	// Verify pattern: each byte should be its offset modulo 256
	for i := range data {
		expected := byte(i % 256)
		assert.Equal(t, expected, data[i], "byte at offset %d should be %d, got %d", i, expected, data[i])
	}
}

func TestCopyFromProcess_MultipleRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	segmentSize := uint64(1024)
	totalSize := segmentSize * 3

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(totalSize), header.PageSize, tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Start helper process
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", totalSize))
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})

	// Read the memory address from the helper process
	var baseAddr uint64
	err = binary.Read(stdout, binary.LittleEndian, &baseAddr)
	require.NoError(t, err)
	stdout.Close()

	// Wait a bit for the process to be ready
	time.Sleep(100 * time.Millisecond)

	// Test copying multiple non-contiguous ranges
	ranges := []block.Range{
		{Start: int64(baseAddr), Size: int64(segmentSize)},
		{Start: int64(baseAddr + segmentSize*2), Size: int64(segmentSize)},
		{Start: int64(baseAddr + segmentSize), Size: int64(segmentSize)},
	}

	err = cache.CopyFromProcess(ctx, cmd.Process.Pid, ranges)
	require.NoError(t, err)

	// Verify the first segment
	data1 := make([]byte, segmentSize)
	n, err := cache.ReadAt(data1, 0)
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	for i := range data1 {
		expected := byte(i % 256)
		assert.Equal(t, expected, data1[i], "first segment, byte at offset %d", i)
	}

	// Verify the second segment (copied to offset segmentSize*2 in cache)
	data2 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data2, int64(segmentSize*2))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	for i := range data2 {
		expected := byte((int(segmentSize*2) + i) % 256)
		assert.Equal(t, expected, data2[i], "second segment, byte at offset %d", i)
	}

	// Verify the third segment (copied to offset segmentSize in cache)
	data3 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data3, int64(segmentSize))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	for i := range data3 {
		expected := byte((int(segmentSize) + i) % 256)
		assert.Equal(t, expected, data3[i], "third segment, byte at offset %d", i)
	}
}

func TestCopyFromProcess_EmptyRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := uint64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(size), int64(header.PageSize), tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Test with empty ranges
	ranges := []block.Range{}
	err = cache.CopyFromProcess(ctx, os.Getpid(), ranges)
	require.NoError(t, err)
}

func TestCopyFromProcess_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	size := uint64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(size), int64(header.PageSize), tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Start helper process
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", size))
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})

	// Read the memory address from the helper process
	var addr uint64
	err = binary.Read(stdout, binary.LittleEndian, &addr)
	require.NoError(t, err)
	stdout.Close()

	// Wait a bit for the process to be ready
	time.Sleep(100 * time.Millisecond)

	// Cancel context immediately
	cancel()

	// Test copying with cancelled context
	ranges := []block.Range{
		{Start: int64(addr), Size: int64(size)},
	}

	err = cache.CopyFromProcess(ctx, cmd.Process.Pid, ranges)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestCopyFromProcess_InvalidPID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := uint64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(size), header.PageSize, tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Test with invalid PID (very high PID that doesn't exist)
	invalidPID := 999999999
	ranges := []block.Range{
		{Start: 0x1000, Size: 1024},
	}

	err = cache.CopyFromProcess(ctx, invalidPID, ranges)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read memory")
}

func TestCopyFromProcess_InvalidAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := uint64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(size), int64(header.PageSize), tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Test with invalid memory address (very high address that fits in int64)
	invalidAddr := int64(0x7FFFFFFF00000000) // Large but valid int64 value
	ranges := []block.Range{
		{Start: invalidAddr, Size: 1024},
	}

	err = cache.CopyFromProcess(ctx, os.Getpid(), ranges)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read memory")
}

func TestCopyFromProcess_ZeroSizeRange(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := uint64(4096)

	// Create cache
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(size), int64(header.PageSize), tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Start helper process
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", size))
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})

	// Read the memory address from the helper process
	var addr uint64
	err = binary.Read(stdout, binary.LittleEndian, &addr)
	require.NoError(t, err)
	stdout.Close()

	// Wait a bit for the process to be ready
	time.Sleep(100 * time.Millisecond)

	// Test with zero-size range
	ranges := []block.Range{
		{Start: int64(addr), Size: 0},
	}

	err = cache.CopyFromProcess(ctx, cmd.Process.Pid, ranges)
	require.NoError(t, err)
}

func TestCopyFromProcess_LargeRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Use a size that exceeds IOV_MAX (typically 1024 on Linux) if we have many small ranges
	// We'll use 1500 ranges to ensure we exceed IOV_MAX
	numRanges := 1500
	rangeSize := uint64(64) // Small ranges

	// Create cache large enough for all ranges
	totalSize := rangeSize * uint64(numRanges)
	tmpFile := t.TempDir() + "/cache"
	cache, err := block.NewCache(int64(totalSize), int64(header.PageSize), tmpFile, false)
	require.NoError(t, err)
	defer cache.Close()

	// Start helper process
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", totalSize))
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	})

	// Read the memory address from the helper process
	var baseAddr uint64
	err = binary.Read(stdout, binary.LittleEndian, &baseAddr)
	require.NoError(t, err)
	stdout.Close()

	// Wait a bit for the process to be ready
	time.Sleep(100 * time.Millisecond)

	// Create many small ranges that exceed IOV_MAX
	ranges := make([]block.Range, numRanges)
	for i := 0; i < numRanges; i++ {
		ranges[i] = block.Range{
			Start: int64(baseAddr) + int64(i)*int64(rangeSize),
			Size:  int64(rangeSize),
		}
	}

	err = cache.CopyFromProcess(ctx, cmd.Process.Pid, ranges)
	require.NoError(t, err)

	// Verify the data was copied correctly
	// Check a few ranges to ensure they were copied
	checkCount := 10
	if numRanges < checkCount {
		checkCount = numRanges
	}
	for i := 0; i < checkCount; i++ {
		offset := int64(i) * int64(rangeSize)
		data := make([]byte, rangeSize)
		n, err := cache.ReadAt(data, offset)
		require.NoError(t, err)
		require.Equal(t, int(rangeSize), n)

		// Verify pattern
		for j := range data {
			expected := byte((int(offset) + j) % 256)
			assert.Equal(t, expected, data[j], "range %d, byte at offset %d", i, j)
		}
	}
}
