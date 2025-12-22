package block

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// helperProcessData contains the data needed to interact with a helper process
type helperProcessData struct {
	cmd          *exec.Cmd
	addr         uint64
	expectedData []byte
}

// startHelperProcess starts a helper process and waits for it to be ready.
// It returns the memory address and expected data for verification.
func startHelperProcess(t *testing.T, ctx context.Context, size uint64) *helperProcessData {
	t.Helper()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestCopyFromProcess_HelperProcess")
	cmd.Env = append(os.Environ(), "GO_TEST_COPY_HELPER_PROCESS=1")
	cmd.Env = append(cmd.Env, fmt.Sprintf("GO_TEST_MEMORY_SIZE=%d", size))
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	// Create pipes for communication
	addrReader, addrWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		addrReader.Close()
	})

	dataReader, dataWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		dataReader.Close()
	})

	readyReader, readyWriter, err := os.Pipe()
	require.NoError(t, err)

	t.Cleanup(func() {
		readyReader.Close()
	})

	cmd.ExtraFiles = []*os.File{
		addrWriter,
		dataWriter,
		readyWriter,
	}

	err = cmd.Start()
	require.NoError(t, err)

	// Start reading goroutines BEFORE closing write ends to ensure they're ready
	addrChan := make(chan uint64, 1)
	addrErrChan := make(chan error, 1)
	go func() {
		var addr uint64
		err := binary.Read(addrReader, binary.LittleEndian, &addr)
		if err != nil {
			addrErrChan <- err

			return
		}
		addrChan <- addr
	}()

	expectedData := make([]byte, size)
	dataErrChan := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(dataReader, expectedData)
		if err != nil {
			dataErrChan <- err

			return
		}
	}()

	readySignal := make(chan struct{}, 1)
	go func() {
		_, err := io.ReadAll(readyReader)
		assert.NoError(t, err)
		readySignal <- struct{}{}
	}()

	// Close the write ends in the parent process after starting
	// The child has its own copies via ExtraFiles (fds 3, 4, 5), so closing these is safe
	addrWriter.Close()
	dataWriter.Close()
	readyWriter.Close()

	t.Cleanup(func() {
		signalErr := cmd.Process.Signal(syscall.SIGUSR1)
		assert.NoError(t, signalErr)

		waitErr := cmd.Wait()
		// It can be either nil, an ExitError, a context.Canceled error, or "signal: killed"
		assert.True(t,
			(waitErr != nil && func(err error) bool {
				var exitErr *exec.ExitError

				return errors.As(err, &exitErr)
			}(waitErr)) ||
				errors.Is(waitErr, context.Canceled) ||
				(waitErr != nil && strings.Contains(waitErr.Error(), "signal: killed")) ||
				waitErr == nil,
			"unexpected error: %v", waitErr,
		)
	})

	// Wait for ready signal (child has written all data by this point)
	select {
	case <-ctx.Done():
		t.Fatalf("context cancelled while waiting for helper process: %v", ctx.Err())
	case <-readySignal:
	}

	// Get the address
	var addr uint64
	select {
	case err := <-addrErrChan:
		t.Fatalf("failed to read address: %v", err)
	case addr = <-addrChan:
	}

	// Check for data read errors
	select {
	case err := <-dataErrChan:
		t.Fatalf("failed to read data: %v", err)
	default:
	}

	addrReader.Close()
	dataReader.Close()

	return &helperProcessData{
		cmd:          cmd,
		addr:         addr,
		expectedData: expectedData,
	}
}

// TestCopyFromProcess_HelperProcess is a helper process that allocates memory
// with known content and waits for the test to read it.
func TestCopyFromProcess_HelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_COPY_HELPER_PROCESS") != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	err := crossProcessHelper()
	if err != nil {
		fmt.Fprintf(os.Stderr, "exit helper process: %v\n", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func crossProcessHelper() error {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	// Allocate memory with known content
	sizeStr := os.Getenv("GO_TEST_MEMORY_SIZE")
	size, err := strconv.ParseUint(sizeStr, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse memory size: %w", err)
	}

	// Allocate memory using mmap
	mem, err := unix.Mmap(-1, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		return fmt.Errorf("failed to mmap memory: %w", err)
	}
	defer unix.Munmap(mem)

	// Fill memory with random data
	_, err = rand.Read(mem)
	if err != nil {
		return fmt.Errorf("failed to generate random data: %w", err)
	}

	// Get file descriptors from ExtraFiles
	// fd 3: addrWriter
	// fd 4: dataWriter
	// fd 5: readyWriter
	addrFile := os.NewFile(uintptr(3), "addr")
	defer addrFile.Close()

	dataFile := os.NewFile(uintptr(4), "data")
	defer dataFile.Close()

	readyFile := os.NewFile(uintptr(5), "ready")
	defer readyFile.Close()

	// Write the memory address (as 8 bytes, little endian)
	addr := uint64(0)
	if len(mem) > 0 {
		addr = uint64(uintptr(unsafe.Pointer(&mem[0])))
	}
	err = binary.Write(addrFile, binary.LittleEndian, addr)
	if err != nil {
		return fmt.Errorf("failed to write address: %w", err)
	}
	addrFile.Close()

	// Write the random data
	_, err = dataFile.Write(mem)
	if err != nil {
		return fmt.Errorf("failed to write random data: %w", err)
	}
	dataFile.Close()

	// Signal ready by closing the ready file
	readyFile.Close()

	// Wait for SIGUSR1 to exit
	exitSignal := make(chan os.Signal, 1)
	signal.Notify(exitSignal, syscall.SIGUSR1)
	defer signal.Stop(exitSignal)

	select {
	case <-ctx.Done():
		return fmt.Errorf("context done: %w: %w", ctx.Err(), context.Cause(ctx))
	case <-exitSignal:
		return nil
	}
}

func TestCopyFromProcess_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	size := int64(4096)

	helper := startHelperProcess(t, ctx, uint64(size))

	// Test copying a single range
	ranges := []Range{
		{Start: int64(helper.addr), Size: size},
	}

	tmpFile := t.TempDir() + "/cache"

	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, helper.cmd.Process.Pid, ranges)
	require.NoError(t, err)

	defer cache.Close()

	// Verify the copied data
	data := make([]byte, size)
	n, err := cache.ReadAt(data, 0)
	require.NoError(t, err)
	require.Equal(t, int(size), n)

	// Verify the data matches the random data exactly
	assert.Equal(t, helper.expectedData, data, "copied data should match the original random data")
}

func TestCopyFromProcess_MultipleRanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	segmentSize := uint64(header.PageSize) // Use PageSize to ensure alignment
	totalSize := segmentSize * 3

	helper := startHelperProcess(t, ctx, totalSize)

	// Test copying multiple non-contiguous ranges
	// Order: 0th, 2nd, then 1st segment in memory
	ranges := []Range{
		{Start: int64(helper.addr), Size: int64(segmentSize)},                 // 0th segment
		{Start: int64(helper.addr + segmentSize*2), Size: int64(segmentSize)}, // 2nd segment
		{Start: int64(helper.addr + segmentSize), Size: int64(segmentSize)},   // 1st segment
	}

	tmpFile := t.TempDir() + "/cache"
	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, helper.cmd.Process.Pid, ranges)
	require.NoError(t, err)
	defer cache.Close()

	// Verify the first segment (at cache offset 0): should be from source baseAddr (0th segment of process)
	data1 := make([]byte, segmentSize)
	n, err := cache.ReadAt(data1, 0)
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected1 := helper.expectedData[0:segmentSize]
	assert.Equal(t, expected1, data1, "first segment should match original random data")

	// Verify the second segment (at cache offset segmentSize): should be from source baseAddr+segmentSize*2 (2nd segment of process)
	data2 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data2, int64(segmentSize))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected2 := helper.expectedData[segmentSize*2 : segmentSize*3]
	assert.Equal(t, expected2, data2, "second segment should match original random data")

	// Verify the third segment (at cache offset segmentSize*2): should be from source baseAddr+segmentSize (1st segment of process)
	data3 := make([]byte, segmentSize)
	n, err = cache.ReadAt(data3, int64(segmentSize*2))
	require.NoError(t, err)
	require.Equal(t, int(segmentSize), n)
	expected3 := helper.expectedData[segmentSize : segmentSize*2]
	assert.Equal(t, expected3, data3, "third segment should match original random data")
}

func TestCopyFromProcess_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	size := uint64(4096)

	helper := startHelperProcess(t, ctx, size)

	// Cancel context immediately
	cancel()

	// Test copying with cancelled context
	ranges := []Range{
		{Start: int64(helper.addr), Size: int64(size)},
	}

	tmpFile := t.TempDir() + "/cache"
	_, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, helper.cmd.Process.Pid, ranges)
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

	helper := startHelperProcess(t, ctx, totalSize)

	// Create many small ranges that exceed IOV_MAX
	ranges := make([]Range, numRanges)
	for i := range numRanges {
		ranges[i] = Range{
			Start: int64(helper.addr) + int64(i)*int64(rangeSize),
			Size:  int64(rangeSize),
		}
	}

	tmpFile := t.TempDir() + "/cache"
	cache, err := NewCacheFromProcessMemory(ctx, header.PageSize, tmpFile, helper.cmd.Process.Pid, ranges)
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
			expectedByte := helper.expectedData[actualOffset+int64(j)]
			assert.Equal(t, expectedByte, data[offsetInBlock+int64(j)], "range %d, byte at offset %d", i, j)
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
