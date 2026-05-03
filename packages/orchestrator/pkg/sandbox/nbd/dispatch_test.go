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

// mockProvider records WriteZeroesAt calls so tests can assert that the
// dispatcher routed an NBD opcode to the right backend method.
type mockProvider struct {
	mu     sync.Mutex
	zeroes []zeroCall
}

type zeroCall struct {
	off, length int64
}

func (m *mockProvider) ReadAt(_ context.Context, p []byte, _ int64) (int, error) {
	clear(p)

	return len(p), nil
}

func (m *mockProvider) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }

func (m *mockProvider) WriteZeroesAt(off, length int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.zeroes = append(m.zeroes, zeroCall{off: off, length: length})

	return int(length), nil
}

func (m *mockProvider) Size(_ context.Context) (int64, error) { return 1 << 20, nil }

func (m *mockProvider) snapshotZeroes() []zeroCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]zeroCall(nil), m.zeroes...)
}

func writeReq(t *testing.T, w io.Writer, opcode uint16, handle, from uint64, length uint32) {
	t.Helper()
	hdr := make([]byte, 28)
	binary.BigEndian.PutUint32(hdr[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint16(hdr[6:8], opcode)
	binary.BigEndian.PutUint64(hdr[8:16], handle)
	binary.BigEndian.PutUint64(hdr[16:24], from)
	binary.BigEndian.PutUint32(hdr[24:28], length)
	_, err := w.Write(hdr)
	require.NoError(t, err)
}

func readResp(t *testing.T, r io.Reader) (errCode uint32, handle uint64) {
	t.Helper()
	hdr := make([]byte, 16)
	_, err := io.ReadFull(r, hdr)
	require.NoError(t, err)
	require.Equal(t, uint32(NBDResponseMagic), binary.BigEndian.Uint32(hdr[0:4]))

	return binary.BigEndian.Uint32(hdr[4:8]), binary.BigEndian.Uint64(hdr[8:16])
}

// dispatchHarness runs Handle on one end of a net.Pipe; tests drive the
// other end synchronously.
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
	prov := &mockProvider{}
	disp := NewDispatch(server, prov)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- disp.Handle(ctx) }()

	h := &dispatchHarness{t: t, client: client, prov: prov, disp: disp, done: done, cancel: cancel}
	t.Cleanup(h.close)

	return h
}

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

func (h *dispatchHarness) disconnectAndWait() {
	h.t.Helper()
	writeReq(h.t, h.client, NBDCmdDisconnect, 0, 0, 0)
	h.close()
}

func TestDispatch_WriteZeroesCommand(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	const length = 1 << 16
	writeReq(t, h.client, NBDCmdWriteZeroes, 42, 4096, length)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(42), handle)

	h.disconnectAndWait()

	zeroes := h.prov.snapshotZeroes()
	require.Len(t, zeroes, 1)
	assert.Equal(t, zeroCall{off: 4096, length: length}, zeroes[0])
}

func TestDispatch_TrimRoutesToWriteZeroes(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t)

	const length = 8192
	writeReq(t, h.client, NBDCmdTrim, 99, 32768, length)
	errCode, handle := readResp(t, h.client)
	assert.Equal(t, uint32(0), errCode)
	assert.Equal(t, uint64(99), handle)

	h.disconnectAndWait()

	zeroes := h.prov.snapshotZeroes()
	require.Len(t, zeroes, 1)
	assert.Equal(t, zeroCall{off: 32768, length: length}, zeroes[0])
}
