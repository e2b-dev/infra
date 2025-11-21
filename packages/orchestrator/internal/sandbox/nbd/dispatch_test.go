package nbd

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockProvider implements the Provider interface for testing
type mockProvider struct {
	data      []byte
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if off >= int64(len(m.data)) {
		return 0, io.EOF
	}

	n = copy(p, m.data[off:])

	return n, nil
}

func (m *mockProvider) WriteAt(p []byte, off int64) (n int, err error) {
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

	return m.readBuf.Read(p)
}

func (m *mockReadWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

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

// TestHandle_DrainDoesNotDeadlock verifies that the Drain() function does not
// hold the shuttingDownLock while waiting for pending operations to complete.
// This test would deadlock or timeout with the old implementation where
// defer d.shuttingDownLock.Unlock() was after d.pendingResponses.Wait().
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

// TestHandle_ConcurrentDrainAndNewRequests verifies that calling Drain()
// doesn't block new cmdRead/cmdWrite calls from checking the shutdown state.
// With the old implementation, Drain() held shuttingDownLock during Wait(),
// which could cause unnecessary blocking.
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
