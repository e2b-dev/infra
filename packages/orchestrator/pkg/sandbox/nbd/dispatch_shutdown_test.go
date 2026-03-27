package nbd

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowProvider simulates a backend that takes a long time to respond to reads.
// This lets us have many in-flight async reads when we cancel the context.
type slowProvider struct {
	readDelay time.Duration
	size      int64
}

func (s *slowProvider) ReadAt(_ context.Context, p []byte, _ int64) (int, error) {
	time.Sleep(s.readDelay)
	clear(p)
	return len(p), nil
}

func (s *slowProvider) WriteAt(p []byte, _ int64) (int, error) {
	time.Sleep(s.readDelay)
	return len(p), nil
}

func (s *slowProvider) Size(_ context.Context) (int64, error) {
	return s.size, nil
}

// writeNBDRequest writes a raw NBD request packet to w.
func writeNBDRequest(w io.Writer, cmdType uint32, handle uint64, from uint64, length uint32) error {
	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint32(header[4:8], cmdType)
	binary.BigEndian.PutUint64(header[8:16], handle)
	binary.BigEndian.PutUint64(header[16:24], from)
	binary.BigEndian.PutUint32(header[24:28], length)
	_, err := w.Write(header)
	return err
}

// readNBDResponse reads a raw NBD response header from r.
func readNBDResponse(r io.Reader) (magic uint32, respErr uint32, handle uint64, err error) {
	header := make([]byte, 16)
	_, err = io.ReadFull(r, header)
	if err != nil {
		return 0, 0, 0, err
	}
	magic = binary.BigEndian.Uint32(header[0:4])
	respErr = binary.BigEndian.Uint32(header[4:8])
	handle = binary.BigEndian.Uint64(header[8:16])
	return magic, respErr, handle, nil
}

// drainReadResponses reads NBD responses (with variable-length data payloads
// for successful reads) from r until it gets an error. It must run concurrently
// with the dispatch to avoid deadlocking on net.Pipe()'s synchronous writes.
func drainReadResponses(r io.Reader, readSize int, count *atomic.Int32, fatalErr *atomic.Value) {
	for {
		_, respErr, _, err := readNBDResponse(r)
		if err != nil {
			return
		}
		count.Add(1)

		// Successful read responses (error==0) include a data payload.
		if respErr == 0 {
			dataBuf := make([]byte, readSize)
			if _, err := io.ReadFull(r, dataBuf); err != nil {
				fatalErr.Store(err)
				return
			}
		}
	}
}

// drainWriteResponses reads NBD write responses (header only, no data) from r
// until it gets an error.
func drainWriteResponses(r io.Reader, count *atomic.Int32) {
	for {
		_, _, _, err := readNBDResponse(r)
		if err != nil {
			return
		}
		count.Add(1)
	}
}

// TestDispatch_ShutdownDuringReads verifies that cancelling the context while
// async reads are in-flight does not produce "use of closed network connection"
// errors. The fix reorders Close() to drain pending responses before closing
// the socket.
func TestDispatch_ShutdownDuringReads(t *testing.T) {
	t.Parallel()

	const numReads = 20
	const readSize = 4096

	prov := &slowProvider{readDelay: 200 * time.Millisecond, size: 10 * 1024 * 1024}

	// net.Pipe() is synchronous — writes block until the other end reads.
	// We must consume responses concurrently to avoid deadlocking.
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	dispatch := NewDispatch(serverConn, prov)

	ctx, cancel := context.WithCancel(context.Background())

	// Start Handle() — reads NBD requests from the socket and dispatches them.
	var handleErr error
	var handleWg sync.WaitGroup
	handleWg.Add(1)
	go func() {
		defer handleWg.Done()
		handleErr = dispatch.Handle(ctx)
	}()

	// Start consuming responses concurrently so writeResponse doesn't block
	// on the synchronous pipe.
	var responseCount atomic.Int32
	var readErr atomic.Value
	go drainReadResponses(clientConn, readSize, &responseCount, &readErr)

	// Send many read requests to build up in-flight async operations.
	for i := range numReads {
		err := writeNBDRequest(clientConn, NBDCmdRead, uint64(i+1), 0, readSize)
		require.NoError(t, err, "failed to write NBD read request %d", i)
	}

	// Give the dispatch time to accept and start processing all requests.
	time.Sleep(50 * time.Millisecond)

	// --- Simulate the fixed Close() sequence ---

	// 1. Cancel context — async goroutines see ctx.Done()
	cancel()

	// 2. Drain — wait for pending responses to be written (socket still open!)
	dispatch.Drain()

	// 3. Now close the socket — unblocks Handle()'s blocking Read()
	serverConn.Close()

	// 4. Wait for Handle() to return
	handleWg.Wait()

	// Close client side to unblock the response reader goroutine.
	clientConn.Close()

	// Give the reader goroutine a moment to finish.
	time.Sleep(50 * time.Millisecond)

	got := int(responseCount.Load())
	t.Logf("received %d/%d responses", got, numReads)
	assert.Equal(t, numReads, got, "should receive a response for every request")

	if v := readErr.Load(); v != nil {
		t.Errorf("unexpected error reading response data: %v", v)
	}

	// Handle() should have returned due to the closed socket (io.EOF or similar),
	// not due to a fatal writeResponse error.
	if handleErr != nil {
		assert.NotContains(t, handleErr.Error(), "use of closed network connection",
			"should not see closed connection error — drain should happen before socket close")
	}
}

// TestDispatch_ShutdownDuringWrites is the same but for write commands.
func TestDispatch_ShutdownDuringWrites(t *testing.T) {
	t.Parallel()

	const numWrites = 20
	const writeSize = 4096

	prov := &slowProvider{readDelay: 200 * time.Millisecond, size: 10 * 1024 * 1024}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	dispatch := NewDispatch(serverConn, prov)

	ctx, cancel := context.WithCancel(context.Background())

	var handleErr error
	var handleWg sync.WaitGroup
	handleWg.Add(1)
	go func() {
		defer handleWg.Done()
		handleErr = dispatch.Handle(ctx)
	}()

	// Consume responses concurrently.
	var responseCount atomic.Int32
	go drainWriteResponses(clientConn, &responseCount)

	// Send write requests (header + data payload).
	payload := make([]byte, writeSize)
	for i := range numWrites {
		err := writeNBDRequest(clientConn, NBDCmdWrite, uint64(i+1), 0, writeSize)
		require.NoError(t, err, "failed to write NBD write request header %d", i)
		_, err = clientConn.Write(payload)
		require.NoError(t, err, "failed to write NBD write data %d", i)
	}

	time.Sleep(50 * time.Millisecond)

	// Fixed Close() sequence
	cancel()
	dispatch.Drain()
	serverConn.Close()
	handleWg.Wait()
	clientConn.Close()

	time.Sleep(50 * time.Millisecond)

	got := int(responseCount.Load())
	t.Logf("received %d/%d responses", got, numWrites)
	assert.Equal(t, numWrites, got, "should receive a response for every request")

	if handleErr != nil {
		assert.NotContains(t, handleErr.Error(), "use of closed network connection",
			"should not see closed connection error")
	}
}
