package block

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

		panic(err)
	}

	// Allocate memory using mmap (similar to testutils.NewPageMmap but simpler)
	mem, err := unix.Mmap(-1, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to mmap memory: %v\n", err)

		panic(err)
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

		panic(err)
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

		panic("helper process timeout")
	}
}

func TestCopyFromProcess_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := int64(4096)

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
	ranges := []Range{
		{Start: int64(addr), Size: size},
	}

	tmpFile := t.TempDir() + "/cache"

	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.NoError(t, err)

	defer cache.Close()

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
	segmentSize := uint64(header.PageSize) // Use PageSize to ensure alignment
	totalSize := segmentSize * 3

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
	ranges := []Range{
		{Start: int64(baseAddr), Size: int64(segmentSize)},
		{Start: int64(baseAddr + segmentSize*2), Size: int64(segmentSize)},
		{Start: int64(baseAddr + segmentSize), Size: int64(segmentSize)},
	}

	tmpFile := t.TempDir() + "/cache"
	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.NoError(t, err)
	defer cache.Close()

	// Verify the first segment (at cache offset 0)
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

func TestCopyFromProcess_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	size := uint64(4096)

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
	ranges := []Range{
		{Start: int64(addr), Size: int64(size)},
	}

	tmpFile := t.TempDir() + "/cache"
	_, err = NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestCopyFromProcess_InvalidPID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Test with invalid PID (very high PID that doesn't exist)
	invalidPID := 999999999
	ranges := []Range{
		{Start: 0x1000, Size: 1024},
	}

	tmpFile := t.TempDir() + "/cache"
	_, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, invalidPID, ranges)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read memory")
}

func TestCopyFromProcess_InvalidAddress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Test with invalid memory address (very high address that fits in int64)
	invalidAddr := int64(0x7FFFFFFF00000000) // Large but valid int64 value
	ranges := []Range{
		{Start: invalidAddr, Size: 1024},
	}

	tmpFile := t.TempDir() + "/cache"
	_, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, os.Getpid(), ranges)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read memory")
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
	ranges := make([]Range, numRanges)
	for i := range numRanges {
		ranges[i] = Range{
			Start: int64(baseAddr) + int64(i)*int64(rangeSize),
			Size:  int64(rangeSize),
		}
	}

	tmpFile := t.TempDir() + "/cache"
	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.NoError(t, err)
	defer cache.Close()

	// Verify the data was copied correctly
	// Check a few ranges to ensure they were copied
	// ReadAt offsets must be multiples of header.PageSize
	checkCount := min(numRanges, 10)
	for i := range checkCount {
		// Calculate the actual offset in cache (ranges are stored sequentially)
		actualOffset := int64(i) * int64(rangeSize)
		// Align offset to header.PageSize boundary
		alignedOffset := (actualOffset / header.PageSize) * header.PageSize
		// Calculate offset within the aligned block
		offsetInBlock := actualOffset - alignedOffset

		// Read a full page to ensure we get the data
		data := make([]byte, header.PageSize)
		fmt.Println("reading at aligned offset", alignedOffset, "with offset in block", offsetInBlock)
		n, err := cache.ReadAt(data, alignedOffset)
		require.NoError(t, err)
		require.Equal(t, int(header.PageSize), n)

		// Verify pattern for the range we're checking
		for j := range rangeSize {
			expected := byte((actualOffset + int64(j)) % 256)
			assert.Equal(t, expected, data[offsetInBlock+int64(j)], "range %d, byte at offset %d", i, j)
		}
	}
}

func TestEmptyRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ranges := []Range{}
	tmpFile := t.TempDir() + "/cache"
	c, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, os.Getpid(), ranges)
	require.NoError(t, err)

	defer c.Close()
}
