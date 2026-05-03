package nbd

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider records every call so the test can assert the dispatch loop
// routed each NBD opcode to the right backend method. The intent is to verify
// protocol-to-backend mapping, not backend semantics, so reads return zeros
// and writes are no-ops on success.
type mockProvider struct {
	mu          sync.Mutex
	writes      []writeCall
	zeroes      []zeroCall
	reads       []readCall
	writeErr    error
	zeroErr     error
	readErr     error
	deviceBytes int64
}

type writeCall struct {
	off  int64
	data []byte
}

type zeroCall struct {
	off, length int64
}

type readCall struct {
	off, length int64
}

func (m *mockProvider) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	m.mu.Lock()
	m.reads = append(m.reads, readCall{off: off, length: int64(len(p))})
	err := m.readErr
	m.mu.Unlock()

	if err != nil {
		return 0, err
	}

	clear(p)

	return len(p), nil
}

func (m *mockProvider) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.writeErr != nil {
		return 0, m.writeErr
	}

	cp := make([]byte, len(p))
	copy(cp, p)
	m.writes = append(m.writes, writeCall{off: off, data: cp})

	return len(p), nil
}

func (m *mockProvider) WriteZeroesAt(off, length int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.zeroErr != nil {
		return 0, m.zeroErr
	}

	m.zeroes = append(m.zeroes, zeroCall{off: off, length: length})

	return int(length), nil
}

func (m *mockProvider) Size(_ context.Context) (int64, error) { return m.deviceBytes, nil }

func (m *mockProvider) snapshot() (writes []writeCall, zeroes []zeroCall) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]writeCall(nil), m.writes...), append([]zeroCall(nil), m.zeroes...)
}

func writeReq(t *testing.T, w io.Writer, opcode uint32, handle, from uint64, length uint32, data []byte) {
	t.Helper()
	hdr := make([]byte, 28)
	binary.BigEndian.PutUint32(hdr[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint32(hdr[4:8], opcode)
	binary.BigEndian.PutUint64(hdr[8:16], handle)
	binary.BigEndian.PutUint64(hdr[16:24], from)
	binary.BigEndian.PutUint32(hdr[24:28], length)
	_, err := w.Write(hdr)
	require.NoError(t, err)
	if len(data) > 0 {
		_, err := w.Write(data)
		require.NoError(t, err)
	}
}

func readResp(t *testing.T, r io.Reader) (errCode uint32, handle uint64) {
	t.Helper()
	hdr := make([]byte, 16)
	_, err := io.ReadFull(r, hdr)
	require.NoError(t, err)
	require.Equal(t, uint32(NBDResponseMagic), binary.BigEndian.Uint32(hdr[0:4]))

	return binary.BigEndian.Uint32(hdr[4:8]), binary.BigEndian.Uint64(hdr[8:16])
}

// dispatchHarness wires a Dispatch to one end of a net.Pipe and runs Handle
// on a goroutine. Test code drives the client end synchronously: every
// readResp blocks until the dispatch's response goroutine writes back.
type dispatchHarness struct {
	t      *testing.T
	client net.Conn
	prov   *mockProvider
	disp   *Dispatch
	done   chan error
	cancel context.CancelFunc

	closeOnce sync.Once
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	client, server := net.Pipe()
	prov := &mockProvider{deviceBytes: 1 << 20}
	disp := NewDispatch(server, prov)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- disp.Handle(ctx) }()

	h := &dispatchHarness{t: t, client: client, prov: prov, disp: disp, done: done, cancel: cancel}
	t.Cleanup(h.close)

	return h
}

// close is safe to call multiple times; whichever exit path runs first
// (clean Disconnect or t.Cleanup fallback) takes the wait, the other no-ops.
func (h *dispatchHarness) close() {
	h.closeOnce.Do(func() {
		h.cancel()
		_ = h.client.Close()
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
			h.t.Error("dispatch did not exit after close")
		}
		h.disp.Drain()
	})
}

// disconnectAndWait sends NBD_CMD_DISCONNECT, waits for Handle to return, and
// drains pending responses so the test can inspect provider state.
func (h *dispatchHarness) disconnectAndWait() {
	h.t.Helper()
	writeReq(h.t, h.client, NBDCmdDisconnect, 0, 0, 0, nil)
	h.close()
}

func TestDispatch_AllZeroWriteRoutesToWriteZeroes(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	const length = 4096
	zeros := make([]byte, length)
	writeReq(t, h.client, NBDCmdWrite, 1, 8192, length, zeros)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(1), handle)

	h.disconnectAndWait()

	writes, zeroes := h.prov.snapshot()
	assert.Empty(t, writes, "all-zero NBD_CMD_WRITE must not reach WriteAt")
	require.Len(t, zeroes, 1)
	assert.Equal(t, zeroCall{off: 8192, length: length}, zeroes[0])
}

func TestDispatch_NonZeroWriteGoesThroughWriteAt(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	payload := []byte("hello-block")
	writeReq(t, h.client, NBDCmdWrite, 7, 16, uint32(len(payload)), payload)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(7), handle)

	h.disconnectAndWait()

	writes, zeroes := h.prov.snapshot()
	assert.Empty(t, zeroes)
	require.Len(t, writes, 1)
	assert.Equal(t, int64(16), writes[0].off)
	assert.Equal(t, payload, writes[0].data)
}

func TestDispatch_WriteZeroesCommand(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	const length = 1 << 16
	writeReq(t, h.client, NBDCmdWriteZeroes, 42, 4096, length, nil)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(42), handle)

	h.disconnectAndWait()

	writes, zeroes := h.prov.snapshot()
	assert.Empty(t, writes)
	require.Len(t, zeroes, 1)
	assert.Equal(t, zeroCall{off: 4096, length: length}, zeroes[0])
}

func TestDispatch_TrimRoutesToWriteZeroes(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	const length = 8192
	writeReq(t, h.client, NBDCmdTrim, 99, 32768, length, nil)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(99), handle)

	h.disconnectAndWait()

	writes, zeroes := h.prov.snapshot()
	assert.Empty(t, writes, "TRIM must not write payload bytes")
	require.Len(t, zeroes, 1)
	assert.Equal(t, zeroCall{off: 32768, length: length}, zeroes[0])
}

func TestDispatch_WriteZeroesBackendErrorReportedInResponse(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)
	h.prov.zeroErr = errors.New("backend boom")

	writeReq(t, h.client, NBDCmdWriteZeroes, 5, 0, 4096, nil)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(1), errCode, "backend errors must surface in the NBD response error byte")
	assert.Equal(t, uint64(5), handle)

	h.disconnectAndWait()
}
