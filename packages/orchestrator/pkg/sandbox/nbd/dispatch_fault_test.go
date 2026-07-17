//go:build linux

package nbd

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

const dispatchFaultChildEnv = "NBD_FAULT_TEST_CHILD"

// replyConn is a fake NBD socket for the real Dispatch.Handle loop: Read
// serves queued request bytes, Write parses 16-byte reply headers onto
// replies. The parser assumes header-only replies, which holds because every
// read in this test fails.
type replyConn struct {
	reqCh   chan []byte
	rbuf    []byte
	wbuf    []byte
	replies chan Response
}

func (c *replyConn) Read(p []byte) (int, error) {
	for len(c.rbuf) == 0 {
		b, ok := <-c.reqCh
		if !ok {
			return 0, io.EOF
		}
		c.rbuf = b
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]

	return n, nil
}

func (c *replyConn) Write(p []byte) (int, error) {
	c.wbuf = append(c.wbuf, p...)
	for len(c.wbuf) >= 16 {
		c.replies <- Response{
			Magic:  binary.BigEndian.Uint32(c.wbuf[0:4]),
			Error:  binary.BigEndian.Uint32(c.wbuf[4:8]),
			Handle: binary.BigEndian.Uint64(c.wbuf[8:16]),
		}
		c.wbuf = c.wbuf[16:]
	}

	return len(p), nil
}

// faultProv serves reads straight from a block.Cache mmap, the way
// *block.Overlay serves cached blocks in production.
type faultProv struct {
	cache *block.Cache
}

func (p *faultProv) ReadAt(_ context.Context, b []byte, off int64) (int, error) {
	return p.cache.ReadAt(b, off)
}

func (p *faultProv) Size(_ context.Context) (int64, error) { return p.cache.Size() }

func (p *faultProv) WriteAt(b []byte, off int64) (int, error) { return p.cache.WriteAt(b, off) }

func (p *faultProv) WriteZeroesAt(off, length int64) (int, error) {
	return p.cache.WriteZeroesAt(off, length)
}

// TestDispatchRead_MmapFault verifies that a SIGBUS from the backend's mmap
// copy is reported as a per-request NBD error reply while the dispatch loop
// keeps serving. Runs in a subprocess because an unguarded fault is a fatal
// runtime error.
func TestDispatchRead_MmapFault(t *testing.T) {
	if os.Getenv(dispatchFaultChildEnv) == "1" {
		dispatchReadFaultChild(t)

		return
	}
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=^TestDispatchRead_MmapFault$", "-test.v")
	cmd.Env = append(os.Environ(), dispatchFaultChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err,
		"child crashed: a backend mmap fault must become an NBD error reply, not kill the process\n%s", out)
}

func dispatchReadFaultChild(t *testing.T) {
	t.Helper()

	const (
		blockSize = int64(4096)
		size      = 2 * blockSize
	)

	path := filepath.Join(t.TempDir(), "cache")
	cache, err := block.NewCache(size, blockSize, path, true)
	require.NoError(t, err)
	defer cache.Close()

	// Mapped pages beyond EOF fault with SIGBUS on access.
	require.NoError(t, os.Truncate(path, 0))

	conn := &replyConn{reqCh: make(chan []byte, 8), replies: make(chan Response, 8)}
	d := NewDispatch(conn, &faultProv{cache: cache}, true)

	done := make(chan error, 1)
	go func() { done <- d.Handle(t.Context()) }()

	// Each read must come back as an error reply, with the loop still alive.
	for handle := uint64(1); handle <= 2; handle++ {
		conn.reqCh <- nbdRequest(NBDCmdRead, handle, 0, uint32(blockSize))
		select {
		case resp := <-conn.replies:
			require.Equal(t, uint32(NBDResponseMagic), resp.Magic)
			require.Equal(t, handle, resp.Handle)
			require.NotZerof(t, resp.Error, "read %d: backend fault must be an NBD error reply", handle)
		case <-time.After(5 * time.Second):
			t.Fatalf("no reply for read %d: dispatch loop is stuck or dead", handle)
		}
	}

	close(conn.reqCh) // Handle's Read returns io.EOF
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch loop did not exit")
	}
}
