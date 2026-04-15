package nbd

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider is a controllable backend for Dispatch tests.
type mockProvider struct {
	size    int64
	delayFn func(off int64) time.Duration // nil = no delay
}

func (m *mockProvider) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if m.delayFn != nil {
		d := m.delayFn(off)
		if d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	}
	clear(p)
	return len(p), nil
}

func (m *mockProvider) WriteAt(p []byte, off int64) (int, error) {
	return len(p), nil
}

func (m *mockProvider) Size(_ context.Context) (int64, error) {
	return m.size, nil
}

// nbdClient acts as a mock kernel. It sends NBD request packets and
// reads/validates response packets.
type nbdClient struct {
	conn net.Conn
	t    *testing.T

	mu         sync.Mutex
	pending    map[uint64]pendingReq // handle → request info
	seen       map[uint64]bool       // handles we already got a response for
	nextHandle atomic.Uint64

	// Tag-level tracking (simulates kernel's per-tag cookie).
	// Maps tag → latest cookie assigned to that tag.
	tagCookies map[uint32]uint32
}

type pendingReq struct {
	cmdType uint32
	length  uint32
	tag     uint32
	cookie  uint32
}

func newNBDClient(t *testing.T, conn net.Conn) *nbdClient {
	return &nbdClient{
		conn:       conn,
		t:          t,
		pending:    make(map[uint64]pendingReq),
		seen:       make(map[uint64]bool),
		tagCookies: make(map[uint32]uint32),
	}
}

// makeHandle builds an NBD handle the same way the kernel does:
// handle = (cookie << 32) | tag
func makeHandle(tag uint32, cookie uint32) uint64 {
	return (uint64(cookie) << 32) | uint64(tag)
}

func handleTag(h uint64) uint32    { return uint32(h) }
func handleCookie(h uint64) uint32 { return uint32(h >> 32) }

// sendReadWithHandle sends a read request with an explicit handle.
func (c *nbdClient) sendReadWithHandle(handle uint64, from uint64, length uint32) {
	tag := handleTag(handle)
	cookie := handleCookie(handle)

	c.mu.Lock()
	c.pending[handle] = pendingReq{
		cmdType: NBDCmdRead,
		length:  length,
		tag:     tag,
		cookie:  cookie,
	}
	c.tagCookies[tag] = cookie // latest cookie for this tag
	c.mu.Unlock()

	var buf [28]byte
	binary.BigEndian.PutUint32(buf[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint32(buf[4:8], NBDCmdRead)
	binary.BigEndian.PutUint64(buf[8:16], handle)
	binary.BigEndian.PutUint64(buf[16:24], from)
	binary.BigEndian.PutUint32(buf[24:28], length)

	_, err := c.conn.Write(buf[:])
	require.NoError(c.t, err)
}

// sendRead sends a read with an auto-generated unique handle.
func (c *nbdClient) sendRead(from uint64, length uint32) uint64 {
	handle := c.nextHandle.Add(1)
	c.sendReadWithHandle(handle, from, length)
	return handle
}

// sendWrite sends a write with an auto-generated unique handle.
func (c *nbdClient) sendWrite(from uint64, data []byte) uint64 {
	handle := c.nextHandle.Add(1)

	c.mu.Lock()
	c.pending[handle] = pendingReq{cmdType: NBDCmdWrite, length: uint32(len(data))}
	c.mu.Unlock()

	combined := make([]byte, 28+len(data))
	binary.BigEndian.PutUint32(combined[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint32(combined[4:8], NBDCmdWrite)
	binary.BigEndian.PutUint64(combined[8:16], handle)
	binary.BigEndian.PutUint64(combined[16:24], from)
	binary.BigEndian.PutUint32(combined[24:28], uint32(len(data)))
	copy(combined[28:], data)

	_, err := c.conn.Write(combined)
	require.NoError(c.t, err)
	return handle
}

func (c *nbdClient) sendDisconnect() {
	var buf [28]byte
	binary.BigEndian.PutUint32(buf[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint32(buf[4:8], NBDCmdDisconnect)
	_, _ = c.conn.Write(buf[:])
}

// readOneResponse reads and validates one NBD response.
func (c *nbdClient) readOneResponse() (handle uint64, respError uint32, err error) {
	var header [16]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return 0, 0, fmt.Errorf("reading response header: %w", err)
	}

	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != NBDResponseMagic {
		return 0, 0, fmt.Errorf("STREAM CORRUPTION: expected magic 0x%08x, got 0x%08x", NBDResponseMagic, magic)
	}

	respError = binary.BigEndian.Uint32(header[4:8])
	handle = binary.BigEndian.Uint64(header[8:16])

	c.mu.Lock()
	req, isPending := c.pending[handle]
	alreadySeen := c.seen[handle]
	if isPending {
		delete(c.pending, handle)
		c.seen[handle] = true
	}
	c.mu.Unlock()

	if alreadySeen {
		return handle, respError, fmt.Errorf("DOUBLE REPLY: handle 0x%x already responded to", handle)
	}
	if !isPending {
		return handle, respError, fmt.Errorf("UNEXPECTED HANDLE: 0x%x not in pending set", handle)
	}

	if req.cmdType == NBDCmdRead && respError == 0 {
		data := make([]byte, req.length)
		if _, err := io.ReadFull(c.conn, data); err != nil {
			return handle, respError, fmt.Errorf("reading data for handle 0x%x: %w", handle, err)
		}
	}

	return handle, respError, nil
}

// readOneResponseCheckTag reads a response and checks the cookie against
// the LATEST cookie assigned to that tag (simulating the kernel's check).
// Returns a "Double reply" error if the response cookie is stale.
func (c *nbdClient) readOneResponseCheckTag() (handle uint64, err error) {
	handle, _, err = c.readOneResponse()
	if err != nil {
		return handle, err
	}

	tag := handleTag(handle)
	respCookie := handleCookie(handle)

	c.mu.Lock()
	latestCookie, exists := c.tagCookies[tag]
	c.mu.Unlock()

	if exists && respCookie != latestCookie {
		return handle, fmt.Errorf(
			"DOUBLE REPLY (kernel perspective): tag=%d, response cookie=%d, current cookie=%d",
			tag, respCookie, latestCookie)
	}

	return handle, nil
}

func (c *nbdClient) readAllResponses(n int) error {
	for i := range n {
		_, _, err := c.readOneResponse()
		if err != nil {
			return fmt.Errorf("response #%d: %w", i, err)
		}
	}
	return nil
}

func setupDispatchPair(t *testing.T, prov Provider) (*nbdClient, *Dispatch, context.Context, context.CancelFunc) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	dispatch := NewDispatch(serverConn, prov)
	client := newNBDClient(t, clientConn)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		serverConn.Close()
		clientConn.Close()
	})
	return client, dispatch, ctx, cancel
}

// ---------------------------------------------------------------------------
// Core protocol correctness tests
// ---------------------------------------------------------------------------

func TestDispatch_ConcurrentSlowReads(t *testing.T) {
	t.Parallel()
	prov := &mockProvider{
		size: 1024 * 1024,
		delayFn: func(off int64) time.Duration {
			if (off/4096)%2 == 0 {
				return 200 * time.Millisecond
			}
			return 0
		},
	}
	client, dispatch, ctx, cancel := setupDispatchPair(t, prov)
	defer cancel()

	handleDone := make(chan error, 1)
	go func() { handleDone <- dispatch.Handle(ctx) }()

	for i := range 64 {
		client.sendRead(uint64(i*4096)%uint64(prov.size), 4096)
	}

	require.NoError(t, client.readAllResponses(64))
	client.sendDisconnect()
	require.NoError(t, <-handleDone)
}

func TestDispatch_MixedReadsWrites(t *testing.T) {
	t.Parallel()
	prov := &mockProvider{size: 1024 * 1024}
	client, dispatch, ctx, cancel := setupDispatchPair(t, prov)
	defer cancel()

	handleDone := make(chan error, 1)
	go func() { handleDone <- dispatch.Handle(ctx) }()

	for i := range 100 {
		if i%3 == 0 {
			client.sendWrite(uint64(i*4096)%uint64(prov.size), make([]byte, 4096))
		} else {
			client.sendRead(uint64(i*4096)%uint64(prov.size), 4096)
		}
	}

	require.NoError(t, client.readAllResponses(100))
	client.sendDisconnect()
	require.NoError(t, <-handleDone)
}

func TestDispatch_LargeWriteThenRead(t *testing.T) {
	t.Parallel()
	prov := &mockProvider{size: 10 * 1024 * 1024}
	client, dispatch, ctx, cancel := setupDispatchPair(t, prov)
	defer cancel()

	handleDone := make(chan error, 1)
	go func() { handleDone <- dispatch.Handle(ctx) }()

	client.sendWrite(0, make([]byte, 5*1024*1024))
	client.sendRead(0, 4096)
	require.NoError(t, client.readAllResponses(2))
	client.sendDisconnect()
	require.NoError(t, <-handleDone)
}

// ---------------------------------------------------------------------------
// Kernel 6.8 bug reproduction
// ---------------------------------------------------------------------------

// TestDispatch_Kernel68_PartialWriteCorruption simulates the corrupted byte
// stream that kernel 6.8 produces when nbd_send_cmd partially sends a WRITE
// header but the data page send fails and the request is requeued.
func TestDispatch_Kernel68_PartialWriteCorruption(t *testing.T) {
	t.Parallel()
	prov := &mockProvider{size: 1024 * 1024}
	serverConn, clientConn := net.Pipe()
	dispatch := NewDispatch(serverConn, prov)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		serverConn.Close()
		clientConn.Close()
	})

	handleDone := make(chan error, 1)
	go func() { handleDone <- dispatch.Handle(ctx) }()

	// Build the corrupted stream:
	// [WRITE header, handle=1, len=4096] [1000 bytes partial data]
	// [WRITE header, handle=2, len=4096] [4096 bytes full data]
	var stream []byte
	for _, req := range []struct {
		handle   uint64
		dataSize int // 0 = only header
	}{
		{1, 1000}, // partial write data
		{2, 4096}, // full write data
	} {
		var hdr [28]byte
		binary.BigEndian.PutUint32(hdr[0:4], NBDRequestMagic)
		binary.BigEndian.PutUint32(hdr[4:8], NBDCmdWrite)
		binary.BigEndian.PutUint64(hdr[8:16], req.handle)
		binary.BigEndian.PutUint64(hdr[16:24], 0)
		binary.BigEndian.PutUint32(hdr[24:28], 4096) // kernel says 4096 bytes
		stream = append(stream, hdr[:]...)
		stream = append(stream, make([]byte, req.dataSize)...)
	}

	go func() { clientConn.Write(stream) }()

	err := <-handleDone
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid MAGIC",
		"Dispatch handler should detect stream misalignment from partial write data")
	t.Logf("Handle exited with: %v", err)
}

// TestDispatch_Kernel68_DoubleReplyExact reproduces the "Double reply"
// mechanism from the kernel's perspective.
//
// Root cause in kernel 6.8 nbd_send_cmd: when sock_sendmsg is
// interrupted (ERESTARTSYS) during a WRITE data-page send AFTER the
// header was already sent, nbd_send_cmd returns BLK_STS_RESOURCE.
// nbd_handle_cmd does NOT set NBD_CMD_INFLIGHT (line 1056 skipped
// because ret != 0) and does NOT shut down the socket (line 1061
// skipped because ret != -EAGAIN). blk-mq calls
// __blk_mq_requeue_request → blk_mq_put_driver_tag → TAG FREED.
//
// The freed tag is immediately reused by a new request B, which
// increments cmd->cmd_cookie (same nbd_cmd PDU, pre-allocated in the
// tag set). The Dispatch handler is still processing the original
// request A (slow, cold cache). When it responds ~8s later with
// the old tag+cookie, recv_work looks up the tag, finds request B,
// and detects the cookie mismatch → "Double reply".
//
// Fixed in kernel 6.14 via NBD_CMD_PARTIAL_SEND which prevents the
// tag from being freed while a partial send is outstanding.
//
// The sequence this test simulates:
//  1. Request A (tag=1, cookie=4) is sent. Dispatch handler is slow.
//  2. Kernel frees tag 1 (BLK_STS_RESOURCE → blk_mq_put_driver_tag).
//  3. Request B (tag=1, cookie=5) reuses the tag.
//  4. Response B (cookie=5) arrives first → kernel completes B.
//  5. Response A (cookie=4) arrives ~8s later → tag 1's current cookie
//     is 5 → "Double reply on req, cmd_cookie 5, handle cookie 4".
//
// We simulate by sending two read requests with the same tag but
// different cookies. The first is slow, the second is fast.
// The mock client validates responses by tag+cookie.
func TestDispatch_Kernel68_DoubleReplyExact(t *testing.T) {
	t.Parallel()

	const (
		deviceSize = 1024 * 1024
		readLen    = 4096
		slowOff    = 0    // offset that triggers slow read
		fastOff    = 4096 // offset that triggers fast read
	)

	prov := &mockProvider{
		size: deviceSize,
		delayFn: func(off int64) time.Duration {
			if off == slowOff {
				return 500 * time.Millisecond
			}
			return 0
		},
	}

	client, dispatch, ctx, cancel := setupDispatchPair(t, prov)
	defer cancel()

	handleDone := make(chan error, 1)
	go func() { handleDone <- dispatch.Handle(ctx) }()

	// Step 1: Send request A — tag=1, cookie=4 (slow offset).
	// The Dispatch handler will take 500ms to respond.
	handleA := makeHandle(1, 4) // tag=1, cookie=4
	client.sendReadWithHandle(handleA, slowOff, readLen)

	// Step 2: Simulate kernel timeout + tag reuse.
	// Timeout fires → request A completed with EIO → tag freed.
	// Send request B on the SAME tag with incremented cookie
	// (as the kernel would after nbd_send_cmd's cookie++).
	time.Sleep(50 * time.Millisecond) // ensure A is dispatched first
	handleB := makeHandle(1, 5)       // tag=1, cookie=5
	client.sendReadWithHandle(handleB, fastOff, readLen)

	// Step 3: Read responses. B should arrive first (fast), A arrives later (slow).
	// The kernel checks: for each response, is the cookie == the LATEST
	// cookie for that tag?
	var doubleReplyDetected bool
	var responses []string

	for i := range 2 {
		handle, err := client.readOneResponseCheckTag()
		if err != nil {
			responses = append(responses, fmt.Sprintf("response %d: handle=0x%x → %v", i, handle, err))
			doubleReplyDetected = true
		} else {
			responses = append(responses, fmt.Sprintf("response %d: handle=0x%x tag=%d cookie=%d OK",
				i, handle, handleTag(handle), handleCookie(handle)))
		}
	}

	for _, r := range responses {
		t.Log(r)
	}

	// The slow response (cookie=4) MUST trigger "Double reply" because
	// by the time it arrives, tag 1's cookie has been updated to 5.
	require.True(t, doubleReplyDetected,
		"Expected 'Double reply' detection: the slow response (cookie=4) should "+
			"arrive after the fast response (cookie=5) updated tag 1's cookie")

	client.sendDisconnect()
	require.NoError(t, <-handleDone)
}
