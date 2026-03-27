package nbd

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"sync"
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

// readNBDResponse reads a raw NBD response packet from r.
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

// TestDispatch_ShutdownDuringReads verifies that cancelling the context while
// async reads are in-flight does not produce "use of closed network connection"
// errors. The fix reorders Close() to drain pending responses before closing
// the socket.
func TestDispatch_ShutdownDuringReads(t *testing.T) {
	t.Parallel()

	const numReads = 20
	const readSize = 4096

	prov := &slowProvider{readDelay: 200 * time.Millisecond, size: 10 * 1024 * 1024}

	// Create a socketpair — client writes requests, server is the Dispatch side.
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	dispatch := NewDispatch(serverConn, prov)

	ctx, cancel := context.WithCancel(context.Background())

	// Start Handle() in a goroutine — it reads NBD requests from the socket.
	var handleErr error
	var handleWg sync.WaitGroup
	handleWg.Add(1)
	go func() {
		defer handleWg.Done()
		handleErr = dispatch.Handle(ctx)
	}()

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

	// Read all responses from the client side. Each in-flight read should have
	// produced a response (either success or error code 1 from ctx.Done()).
	// The key assertion: we get responses without writeResponse failing.
	var responses []uint64
	for {
		clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		magic, _, handle, err := readNBDResponse(clientConn)
		if err != nil {
			break // No more responses
		}
		assert.Equal(t, uint32(NBDResponseMagic), magic, "bad response magic")
		responses = append(responses, handle)

		// Read requests also send data payload back — read and discard it.
		// Error responses (code 1) have no data, successful ones have readSize bytes.
		// Since we cancelled quickly, most will be error responses with no data,
		// but some may have completed — try to read the data payload.
		dataBuf := make([]byte, readSize)
		clientConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		io.ReadFull(clientConn, dataBuf) //nolint:errcheck // best-effort drain
	}

	t.Logf("received %d/%d responses", len(responses), numReads)
	assert.NotEmpty(t, responses, "should have received at least some responses")

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

	// Read responses
	var responses []uint64
	for {
		clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		magic, _, handle, err := readNBDResponse(clientConn)
		if err != nil {
			break
		}
		assert.Equal(t, uint32(NBDResponseMagic), magic, "bad response magic")
		responses = append(responses, handle)
	}

	t.Logf("received %d/%d responses", len(responses), numWrites)
	assert.NotEmpty(t, responses, "should have received at least some responses")

	if handleErr != nil {
		assert.NotContains(t, handleErr.Error(), "use of closed network connection",
			"should not see closed connection error")
	}
}
