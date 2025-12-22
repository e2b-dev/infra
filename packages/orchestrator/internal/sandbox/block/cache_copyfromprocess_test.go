package block

import (
	"context"
	"crypto/rand"
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

	// Fill memory with random data
	_, err = rand.Read(mem)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate random data: %v\n", err)

		panic(err)
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

	// Write the random data to stdout so the test can verify it
	_, err = os.Stdout.Write(mem)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to write random data: %v\n", err)

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

// waitForProcess consistently checks for process existence until it is confirmed or timeout is reached.
func waitForProcess(pid int, maxWait time.Duration) error {
	start := time.Now()
	for {
		// Try sending signal 0 to the process: if it exists, this succeeds (unless permission is denied).
		err := syscall.Kill(pid, 0)
		if err == nil || err == syscall.EPERM {
			// Process exists (but maybe we lack permission, still good enough for tests)
			return nil
		}
		if time.Since(start) > maxWait {
			return fmt.Errorf("process %d not available after %.2fs", pid, maxWait.Seconds())
		}
		time.Sleep(10 * time.Millisecond)
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

	// Wait until the process is up and running before interacting with it.
	require.NoError(t, waitForProcess(cmd.Process.Pid, 2*time.Second))

	// Read the memory address from the helper process
	var addr uint64
	err = binary.Read(stdout, binary.LittleEndian, &addr)
	require.NoError(t, err)

	// Read the random data that was written to memory
	expectedData := make([]byte, size)
	_, err = stdout.Read(expectedData)
	require.NoError(t, err)
	stdout.Close()

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

	// Verify the data matches the random data exactly
	assert.Equal(t, expectedData, data, "copied data should match the original random data")
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

	// Wait until the process is up and running before interacting with it.
	require.NoError(t, waitForProcess(cmd.Process.Pid, 2*time.Second))

	// Read the memory address from the helper process
	var baseAddr uint64
	err = binary.Read(stdout, binary.LittleEndian, &baseAddr)
	require.NoError(t, err)

	// Read the random data that was written to memory
	expectedData := make([]byte, totalSize)
	_, err = stdout.Read(expectedData)
	require.NoError(t, err)
	stdout.Close()

	// Test copying multiple non-contiguous ranges
	// Order: 0th, 2nd, then 1st segment in memory
	ranges := []Range{
		{Start: int64(baseAddr), Size: int64(segmentSize)},                 // 0th segment
		{Start: int64(baseAddr + segmentSize*2), Size: int64(segmentSize)}, // 2nd segment
		{Start: int64(baseAddr + segmentSize), Size: int64(segmentSize)},   // 1st segment
	}

	tmpFile := t.TempDir() + "/cache"
	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.NoError(t, err)
	defer cache.Close()

	// Verify the first segment (at cache offset 0): should be from source baseAddr (0th segment of process)
	data1 := make([]byte, segmentSize)
	n, err := cache.ReadAt(data1, 0)
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected1 := expectedData[0:segmentSize]
	assert.Equal(t, expected1, data1, "first segment should match original random data")

	// Verify the second segment (at cache offset segmentSize): should be from source baseAddr+segmentSize*2 (2nd segment of process)
	data2 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data2, int64(segmentSize))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected2 := expectedData[segmentSize*2 : segmentSize*3]
	assert.Equal(t, expected2, data2, "second segment should match original random data")

	// Verify the third segment (at cache offset segmentSize*2): should be from source baseAddr+segmentSize (1st segment of process)
	data3 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data3, int64(segmentSize*2))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected3 := expectedData[segmentSize : segmentSize*2]
	assert.Equal(t, expected3, data3, "third segment should match original random data")
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

	// Wait until the process is up and running before interacting with it.
	require.NoError(t, waitForProcess(cmd.Process.Pid, 2*time.Second))

	// Read the memory address from the helper process
	var addr uint64
	err = binary.Read(stdout, binary.LittleEndian, &addr)
	require.NoError(t, err)

	// Read the random data (even though we won't use it, we need to consume it from stdout)
	expectedData := make([]byte, size)
	_, err = stdout.Read(expectedData)
	require.NoError(t, err)
	stdout.Close()

	// Cancel context immediately
	cancel()

	// Test copying with cancelled context
	ranges := []Range{
		{Start: int64(addr), Size: int64(size)},
	}

	tmpFile := t.TempDir() + "/cache"
	_, err = NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, cmd.Process.Pid, ranges)
	require.ErrorIs(t, err, context.Canceled)
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

	// Add a wait here, but since the PID doesn't exist, it will timeout (this is fine for this test).
	_ = waitForProcess(invalidPID, 10*time.Millisecond)

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

	require.NoError(t, waitForProcess(os.Getpid(), 2*time.Second)) // Make sure our process is alive

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

	// Wait until the process is up and running before interacting with it.
	require.NoError(t, waitForProcess(cmd.Process.Pid, 2*time.Second))

	// Read the memory address from the helper process
	var baseAddr uint64
	err = binary.Read(stdout, binary.LittleEndian, &baseAddr)
	require.NoError(t, err)

	// Read the random data that was written to memory
	expectedData := make([]byte, totalSize)
	_, err = stdout.Read(expectedData)
	require.NoError(t, err)
	stdout.Close()

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

		n, err := cache.ReadAt(data, alignedOffset)
		require.NoError(t, err)
		require.Equal(t, int(header.PageSize), n)

		// Verify the range we're checking matches the expected random data
		for j := range rangeSize {
			expectedByte := expectedData[actualOffset+int64(j)]
			assert.Equal(t, expectedByte, data[offsetInBlock+int64(j)], "range %d, byte at offset %d", i, j)
		}
	}
}

func TestEmptyRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ranges := []Range{}
	tmpFile := t.TempDir() + "/cache"
	require.NoError(t, waitForProcess(os.Getpid(), 2*time.Second)) // Make sure our process is alive
	c, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, os.Getpid(), ranges)
	require.NoError(t, err)

	defer c.Close()
}
