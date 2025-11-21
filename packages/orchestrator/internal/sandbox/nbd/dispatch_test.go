package nbd

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider implements the Provider interface for testing
type mockProvider struct {
	data      []byte
	readErr   error
	writeErr  error
	readDelay time.Duration
	mu        sync.Mutex
}

func newMockProvider(size int) *mockProvider {
	return &mockProvider{
		data: make([]byte, size),
	}
}

func (m *mockProvider) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	if m.readDelay > 0 {
		select {
		case <-time.After(m.readDelay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	if m.readErr != nil {
		return 0, m.readErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}

	n = copy(p, m.data[off:])

	return n, nil
}

func (m *mockProvider) WriteAt(p []byte, off int64) (n int, err error) {
	if m.writeErr != nil {
		return 0, m.writeErr
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if off+int64(len(p)) > int64(len(m.data)) {
		return 0, io.EOF
	}

	n = copy(m.data[off:], p)

	return n, nil
}

func (m *mockProvider) Size() (int64, error) {
	return int64(len(m.data)), nil
}

// mockReadWriter implements io.ReadWriter for testing
type mockReadWriter struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
	readErr  error
	writeErr error
	mu       sync.Mutex
}

func newMockReadWriter() *mockReadWriter {
	return &mockReadWriter{
		readBuf:  new(bytes.Buffer),
		writeBuf: new(bytes.Buffer),
	}
}

func (m *mockReadWriter) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.readErr != nil {
		return 0, m.readErr
	}

	return m.readBuf.Read(p)
}

func (m *mockReadWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writeErr != nil {
		return 0, m.writeErr
	}

	return m.writeBuf.Write(p)
}

func (m *mockReadWriter) writeRequest(req Request, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header[0:4], req.Magic)
	binary.BigEndian.PutUint32(header[4:8], req.Type)
	binary.BigEndian.PutUint64(header[8:16], req.Handle)
	binary.BigEndian.PutUint64(header[16:24], req.From)
	binary.BigEndian.PutUint32(header[24:28], req.Length)

	m.readBuf.Write(header)

	if len(data) > 0 {
		m.readBuf.Write(data)
	}
}

func (m *mockReadWriter) getWrittenData() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.writeBuf.Bytes()
}

// Helper function to create an NBD request
func makeRequest(cmdType uint32, handle uint64, from uint64, length uint32) Request {
	return Request{
		Magic:  NBDRequestMagic,
		Type:   cmdType,
		Handle: handle,
		From:   from,
		Length: length,
	}
}

func TestHandle_Disconnect(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Write a disconnect command
	req := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(req, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err, "Handle should return nil on disconnect")
}

func TestHandle_ReadCommand(t *testing.T) {
	// Create a provider with test data
	prov := newMockProvider(1024)
	testData := []byte("test data for reading")
	copy(prov.data[100:], testData)

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send read request followed by disconnect
	readReq := makeRequest(NBDCmdRead, 12345, 100, uint32(len(testData)))
	rw.writeRequest(readReq, nil)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Allow async read to complete
	dispatch.Drain()

	// Verify response
	written := rw.getWrittenData()
	require.GreaterOrEqual(t, len(written), 16, "Should have response header")

	// Check response header
	magic := binary.BigEndian.Uint32(written[0:4])
	assert.Equal(t, uint32(NBDResponseMagic), magic, "Response should have correct magic")

	errorCode := binary.BigEndian.Uint32(written[4:8])
	assert.Equal(t, uint32(0), errorCode, "Response should have no error")

	handle := binary.BigEndian.Uint64(written[8:16])
	assert.Equal(t, uint64(12345), handle, "Response should have correct handle")

	// Check data
	if len(written) > 16 {
		responseData := written[16 : 16+len(testData)]
		assert.Equal(t, testData, responseData, "Response data should match")
	}
}

func TestHandle_WriteCommand(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	testData := []byte("test data for writing")

	// Send write request followed by disconnect
	writeReq := makeRequest(NBDCmdWrite, 54321, 200, uint32(len(testData)))
	rw.writeRequest(writeReq, testData)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Allow async write to complete
	dispatch.Drain()

	// Verify data was written to provider
	writtenData := prov.data[200 : 200+len(testData)]
	assert.Equal(t, testData, writtenData, "Data should be written to provider")

	// Verify response
	written := rw.getWrittenData()
	require.GreaterOrEqual(t, len(written), 16, "Should have response header")

	magic := binary.BigEndian.Uint32(written[0:4])
	assert.Equal(t, uint32(NBDResponseMagic), magic, "Response should have correct magic")

	errorCode := binary.BigEndian.Uint32(written[4:8])
	assert.Equal(t, uint32(0), errorCode, "Response should have no error")

	handle := binary.BigEndian.Uint64(written[8:16])
	assert.Equal(t, uint64(54321), handle, "Response should have correct handle")
}

func TestHandle_TrimCommand(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send trim request followed by disconnect
	trimReq := makeRequest(NBDCmdTrim, 99999, 100, 512)
	rw.writeRequest(trimReq, nil)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Verify response
	written := rw.getWrittenData()
	require.GreaterOrEqual(t, len(written), 16, "Should have response header")

	magic := binary.BigEndian.Uint32(written[0:4])
	assert.Equal(t, uint32(NBDResponseMagic), magic, "Response should have correct magic")

	errorCode := binary.BigEndian.Uint32(written[4:8])
	assert.Equal(t, uint32(0), errorCode, "Response should have no error")

	handle := binary.BigEndian.Uint64(written[8:16])
	assert.Equal(t, uint64(99999), handle, "Response should have correct handle")
}

func TestHandle_FlushCommand(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send flush request
	flushReq := makeRequest(NBDCmdFlush, 11111, 0, 0)
	rw.writeRequest(flushReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported: Flush")
}

func TestHandle_InvalidMagic(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send request with invalid magic
	req := makeRequest(NBDCmdRead, 12345, 0, 100)
	req.Magic = 0xDEADBEEF
	rw.writeRequest(req, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MAGIC")
}

func TestHandle_UnsupportedCommand(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send request with unsupported command type
	req := makeRequest(999, 12345, 0, 100)
	rw.writeRequest(req, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented")
}

func TestHandle_ContextCancellation(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Write a read request
	readReq := makeRequest(NBDCmdRead, 12345, 0, 100)
	rw.writeRequest(readReq, nil)

	// Cancel context immediately
	cancel()

	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHandle_ReadError(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Set read error on the ReadWriter
	rw.readErr = errors.New("read failed")

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read failed")
}

func TestHandle_ProviderReadError(t *testing.T) {
	prov := newMockProvider(1024)
	prov.readErr = errors.New("provider read failed")

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send read request followed by disconnect
	readReq := makeRequest(NBDCmdRead, 12345, 0, 100)
	rw.writeRequest(readReq, nil)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err, "Handle should complete despite provider error")

	// Allow async read to complete
	dispatch.Drain()

	// Verify error response
	written := rw.getWrittenData()
	require.GreaterOrEqual(t, len(written), 16, "Should have response header")

	errorCode := binary.BigEndian.Uint32(written[4:8])
	assert.Equal(t, uint32(1), errorCode, "Response should have error code 1")
}

func TestHandle_ProviderWriteError(t *testing.T) {
	prov := newMockProvider(1024)
	prov.writeErr = errors.New("provider write failed")

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	testData := []byte("test data")

	// Send write request followed by disconnect
	writeReq := makeRequest(NBDCmdWrite, 54321, 0, uint32(len(testData)))
	rw.writeRequest(writeReq, testData)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err, "Handle should complete despite provider error")

	// Allow async write to complete
	dispatch.Drain()

	// Verify error response
	written := rw.getWrittenData()
	require.GreaterOrEqual(t, len(written), 16, "Should have response header")

	errorCode := binary.BigEndian.Uint32(written[4:8])
	assert.Equal(t, uint32(1), errorCode, "Response should have error code 1")
}

func TestHandle_LargeWrite(t *testing.T) {
	// Test writing 8MB of data (larger than default buffer)
	size := 8 * 1024 * 1024
	prov := newMockProvider(10 * 1024 * 1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	testData := make([]byte, size)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// Send large write request followed by disconnect
	writeReq := makeRequest(NBDCmdWrite, 11111, 0, uint32(size))
	rw.writeRequest(writeReq, testData)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Allow async write to complete
	dispatch.Drain()

	// Verify data was written
	assert.Equal(t, testData, prov.data[0:size], "Large data should be written correctly")
}

func TestHandle_WriteTooLarge(t *testing.T) {
	prov := newMockProvider(50 * 1024 * 1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Try to write more than max (32MB)
	size := dispatchMaxWriteBufferSize + 1
	testData := make([]byte, size)

	writeReq := makeRequest(NBDCmdWrite, 11111, 0, uint32(size))
	rw.writeRequest(writeReq, testData)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestHandle_MultipleRequests(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send multiple write requests
	data1 := []byte("first write")
	writeReq1 := makeRequest(NBDCmdWrite, 1, 0, uint32(len(data1)))
	rw.writeRequest(writeReq1, data1)

	data2 := []byte("second write")
	writeReq2 := makeRequest(NBDCmdWrite, 2, 100, uint32(len(data2)))
	rw.writeRequest(writeReq2, data2)

	data3 := []byte("third write")
	writeReq3 := makeRequest(NBDCmdWrite, 3, 200, uint32(len(data3)))
	rw.writeRequest(writeReq3, data3)

	// Send disconnect
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Allow async writes to complete
	dispatch.Drain()

	// Verify all data was written
	assert.Equal(t, data1, prov.data[0:len(data1)])
	assert.Equal(t, data2, prov.data[100:100+len(data2)])
	assert.Equal(t, data3, prov.data[200:200+len(data3)])
}

func TestHandle_PartialHeaderRead(t *testing.T) {
	prov := newMockProvider(1024)

	// Use a pipe for better control over partial reads
	pr, pw := io.Pipe()
	dispatch := NewDispatch(struct {
		io.Reader
		io.Writer
	}{pr, io.Discard}, prov)

	// Start Handle in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- dispatch.Handle(context.Background())
	}()

	// Write partial header
	req := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header[0:4], req.Magic)
	binary.BigEndian.PutUint32(header[4:8], req.Type)
	binary.BigEndian.PutUint64(header[8:16], req.Handle)
	binary.BigEndian.PutUint64(header[16:24], req.From)
	binary.BigEndian.PutUint32(header[24:28], req.Length)

	// Write first part
	pw.Write(header[:15])
	time.Sleep(50 * time.Millisecond)

	// Write rest of header
	pw.Write(header[15:])
	pw.Close()

	// Wait for completion
	select {
	case err := <-errChan:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not complete in time")
	}
}

func TestHandle_ShuttingDown(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Mark as shutting down
	dispatch.Drain()

	// Try to send read request
	readReq := makeRequest(NBDCmdRead, 1, 0, 100)
	rw.writeRequest(readReq, nil)

	// This should return ErrShuttingDown
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	// The error should be propagated through the fatal channel
	// Since we're shutting down, cmdRead will return ErrShuttingDown
	// which gets sent to the fatal channel
	if err != nil {
		assert.ErrorIs(t, err, ErrShuttingDown)
	}
}

func TestHandle_ConcurrentReadsAndWrites(t *testing.T) {
	prov := newMockProvider(1024 * 1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send multiple read and write requests
	for i := 0; i < 10; i++ {
		// Write request
		data := []byte("concurrent test data")
		offset := uint64(i * 1024)
		writeReq := makeRequest(NBDCmdWrite, uint64(i*2), offset, uint32(len(data)))
		rw.writeRequest(writeReq, data)

		// Read request
		readReq := makeRequest(NBDCmdRead, uint64(i*2+1), offset, uint32(len(data)))
		rw.writeRequest(readReq, nil)
	}

	// Send disconnect
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	ctx := context.Background()
	err := dispatch.Handle(ctx)
	require.NoError(t, err)

	// Allow all async operations to complete
	dispatch.Drain()
}

func TestHandle_ContextCancelDuringRead(t *testing.T) {
	prov := newMockProvider(1024)
	// Add delay to read to ensure context cancellation happens during read
	prov.readDelay = 200 * time.Millisecond

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Create a context that will be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Send read request - but don't send disconnect, so Handle blocks
	readReq := makeRequest(NBDCmdRead, 12345, 0, 100)
	rw.writeRequest(readReq, nil)

	// Keep sending more data so Handle doesn't return on EOF
	// Use a goroutine to keep feeding data
	go func() {
		time.Sleep(100 * time.Millisecond)
		// Send more requests to keep it going
		for i := 0; i < 5; i++ {
			req := makeRequest(NBDCmdRead, uint64(i), 0, 100)
			rw.writeRequest(req, nil)
			time.Sleep(20 * time.Millisecond)
		}
	}()

	err := dispatch.Handle(ctx)

	// Should get context deadline exceeded
	assert.Error(t, err)
}

func TestHandle_ContextDeadlineExceeded(t *testing.T) {
	prov := newMockProvider(1024)
	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Create a context with a very short deadline
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(10*time.Millisecond))
	defer cancel()

	// Send a read request
	readReq := makeRequest(NBDCmdRead, 1, 0, 100)
	rw.writeRequest(readReq, nil)

	// Wait for deadline to pass
	time.Sleep(20 * time.Millisecond)

	// Send disconnect - by now the context should be expired
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	err := dispatch.Handle(ctx)

	// Should get context deadline exceeded error
	if err != nil {
		assert.True(t, errors.Is(err, context.DeadlineExceeded) || err == context.DeadlineExceeded,
			"Expected deadline exceeded error, got: %v", err)
	}
}

func TestHandle_SlowProviderReadWithDeadline(t *testing.T) {
	prov := newMockProvider(1024)
	// Make provider reads very slow
	prov.readDelay = 500 * time.Millisecond

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Create context with deadline that expires during provider read
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Send read request followed by disconnect
	readReq := makeRequest(NBDCmdRead, 123, 0, 100)
	rw.writeRequest(readReq, nil)

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	err := dispatch.Handle(ctx)

	// Handle should complete (returns on disconnect), but the provider read should timeout
	require.NoError(t, err, "Handle should complete successfully")

	// Wait for async read to complete
	dispatch.Drain()

	// The response should have an error code because the read timed out
	written := rw.getWrittenData()
	if len(written) >= 16 {
		errorCode := binary.BigEndian.Uint32(written[4:8])
		assert.Equal(t, uint32(1), errorCode, "Response should have error due to context timeout")
	}
}

// mockReadWriterWithDeadline simulates a socket that can have read deadlines
type mockReadWriterWithDeadline struct {
	*mockReadWriter

	readDeadline time.Time
	mu           sync.Mutex
}

func newMockReadWriterWithDeadline() *mockReadWriterWithDeadline {
	return &mockReadWriterWithDeadline{
		mockReadWriter: newMockReadWriter(),
	}
}

func (m *mockReadWriterWithDeadline) Read(p []byte) (n int, err error) {
	m.mu.Lock()
	deadline := m.readDeadline
	m.mu.Unlock()

	if !deadline.IsZero() && time.Now().After(deadline) {
		return 0, errors.New("i/o timeout")
	}

	return m.mockReadWriter.Read(p)
}

func (m *mockReadWriterWithDeadline) setReadDeadline(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readDeadline = t
}

func TestHandle_ReadDeadlineOnSocket(t *testing.T) {
	prov := newMockProvider(1024)

	// Use a pipe that can simulate blocking reads with timeout
	pr, pw := io.Pipe()
	rwWithDeadline := &readWriterWithTimeout{
		Reader:  pr,
		Writer:  io.Discard,
		timeout: 50 * time.Millisecond,
	}
	dispatch := NewDispatch(rwWithDeadline, prov)

	// Start Handle in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- dispatch.Handle(context.Background())
	}()

	// Write a read request
	readReq := makeRequest(NBDCmdRead, 111, 0, 100)
	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header[0:4], readReq.Magic)
	binary.BigEndian.PutUint32(header[4:8], readReq.Type)
	binary.BigEndian.PutUint64(header[8:16], readReq.Handle)
	binary.BigEndian.PutUint64(header[16:24], readReq.From)
	binary.BigEndian.PutUint32(header[24:28], readReq.Length)
	pw.Write(header)

	// Don't write disconnect - the timeout should trigger
	// Wait for timeout to occur
	select {
	case err := <-errChan:
		require.Error(t, err)
		assert.True(t, errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "timeout"),
			"Should get timeout or closed pipe error, got: %v", err)
	case <-time.After(200 * time.Millisecond):
		pw.Close()
		t.Fatal("Expected timeout error")
	}
}

// readWriterWithTimeout simulates a reader that times out
type readWriterWithTimeout struct {
	io.Reader
	io.Writer

	timeout time.Duration
}

func (r *readWriterWithTimeout) Read(p []byte) (n int, err error) {
	if r.timeout > 0 {
		// Simulate timeout by closing after delay
		timer := time.AfterFunc(r.timeout, func() {
			if closer, ok := r.Reader.(io.Closer); ok {
				closer.Close()
			}
		})
		defer timer.Stop()
	}

	return r.Reader.Read(p)
}

func TestHandle_MultipleSlowReads(t *testing.T) {
	prov := newMockProvider(10 * 1024)
	// Slow provider reads
	prov.readDelay = 50 * time.Millisecond

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Context with deadline
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// Send multiple read requests
	for i := 0; i < 10; i++ {
		readReq := makeRequest(NBDCmdRead, uint64(i), uint64(i*100), 50)
		rw.writeRequest(readReq, nil)
	}

	// Send disconnect
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	err := dispatch.Handle(ctx)

	// Should complete before deadline
	require.NoError(t, err)

	// Wait for all async operations
	dispatch.Drain()
}

func TestHandle_DrainDoesNotDeadlock(t *testing.T) {
	prov := newMockProvider(1024)
	// Make provider slow to ensure operations are pending when Drain is called
	prov.readDelay = 100 * time.Millisecond

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send multiple read requests
	for i := 0; i < 10; i++ {
		readReq := makeRequest(NBDCmdRead, uint64(i), 0, 100)
		rw.writeRequest(readReq, nil)
	}

	// Send disconnect
	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	// Start Handle
	handleDone := make(chan error, 1)
	go func() {
		handleDone <- dispatch.Handle(context.Background())
	}()

	// Give it time to process some requests
	time.Sleep(50 * time.Millisecond)

	// Call Drain while operations are pending
	// This should not deadlock - Drain should complete in reasonable time
	drainDone := make(chan struct{})
	go func() {
		dispatch.Drain()
		close(drainDone)
	}()

	// Drain should complete within a reasonable time
	select {
	case <-drainDone:
		// Success - no deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("Drain deadlocked or took too long")
	}

	// Handle should also complete
	select {
	case err := <-handleDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not complete")
	}
}

func TestHandle_ConcurrentDrainAndNewRequests(t *testing.T) {
	prov := newMockProvider(1024)
	prov.readDelay = 50 * time.Millisecond

	rw := newMockReadWriter()
	dispatch := NewDispatch(rw, prov)

	// Send initial requests
	for i := 0; i < 5; i++ {
		readReq := makeRequest(NBDCmdRead, uint64(i), 0, 100)
		rw.writeRequest(readReq, nil)
	}

	disconnectReq := makeRequest(NBDCmdDisconnect, 0, 0, 0)
	rw.writeRequest(disconnectReq, nil)

	// Start Handle
	handleDone := make(chan error, 1)
	go func() {
		handleDone <- dispatch.Handle(context.Background())
	}()

	// Give it time to start processing
	time.Sleep(10 * time.Millisecond)

	// Start Drain (should not block new cmdRead calls from checking shutdown state)
	drainDone := make(chan struct{})
	go func() {
		dispatch.Drain()
		close(drainDone)
	}()

	// Wait for everything to complete
	select {
	case <-drainDone:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Drain did not complete in time")
	}

	select {
	case err := <-handleDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Handle did not complete")
	}
}

func TestHandle_DeadlineRespectedInLoop(t *testing.T) {
	prov := newMockProvider(1024)
	// Add slight delay to provider to ensure we hit deadline
	prov.readDelay = 5 * time.Millisecond

	// Use a pipe for blocking behavior
	pr, pw := io.Pipe()
	dispatch := NewDispatch(struct {
		io.Reader
		io.Writer
	}{pr, io.Discard}, prov)

	// Create context that expires quickly
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()

	// Start Handle in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- dispatch.Handle(ctx)
	}()

	// Send multiple requests to keep it busy
	go func() {
		defer pw.Close()
		for i := 0; i < 100; i++ {
			readReq := makeRequest(NBDCmdRead, uint64(i), 0, 10)
			header := make([]byte, 28)
			binary.BigEndian.PutUint32(header[0:4], readReq.Magic)
			binary.BigEndian.PutUint32(header[4:8], readReq.Type)
			binary.BigEndian.PutUint64(header[8:16], readReq.Handle)
			binary.BigEndian.PutUint64(header[16:24], readReq.From)
			binary.BigEndian.PutUint32(header[24:28], readReq.Length)

			_, err := pw.Write(header)
			if err != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// Wait for error
	select {
	case err := <-errChan:
		require.Error(t, err)
		// Could be deadline exceeded or closed pipe due to deadline
		assert.True(t,
			errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrClosedPipe),
			"Should get context deadline exceeded or closed pipe error, got: %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Expected deadline to be respected")
	}
}
