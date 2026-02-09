package handler

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

type blockingWriteCloser struct {
	writeStarted chan struct{}
	unblockWrite chan struct{}
	closeCalled  chan struct{}

	once sync.Once
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{
		writeStarted: make(chan struct{}),
		unblockWrite: make(chan struct{}),
		closeCalled:  make(chan struct{}),
	}
}

func (b *blockingWriteCloser) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.writeStarted) })
	<-b.unblockWrite
	return len(p), nil
}

func (b *blockingWriteCloser) Close() error {
	select {
	case <-b.closeCalled:
		// already closed
	default:
		close(b.closeCalled)
		close(b.unblockWrite)
	}
	return nil
}

func TestCloseStdinDoesNotDeadlockWithConcurrentWrite(t *testing.T) {
	bw := newBlockingWriteCloser()

	h := &Handler{
		stdin: bw,
		cmd: &exec.Cmd{
			Process: &os.Process{Pid: 1234},
		},
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- h.WriteStdin([]byte("x"))
	}()

	select {
	case <-bw.writeStarted:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for WriteStdin to start blocking")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- h.CloseStdin()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseStdin returned error: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("CloseStdin appears to be blocked (possible deadlock)")
	}

	select {
	case <-bw.closeCalled:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for Close() to be called on stdin")
	}

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("WriteStdin returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for WriteStdin to return after CloseStdin")
	}

	// After CloseStdin, stdin should be treated as closed.
	if err := h.WriteStdin([]byte("x")); err == nil {
		t.Fatal("expected WriteStdin to fail after CloseStdin")
	}
}
