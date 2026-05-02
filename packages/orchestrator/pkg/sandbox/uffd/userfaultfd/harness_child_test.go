package userfaultfd

import (
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"sync"
	"testing"
)

func TestHelperServingProcess(t *testing.T) {
	t.Parallel()

	if os.Getenv(envHelperFlag) != "1" {
		t.Skip("this is a helper process, skipping direct execution")
	}

	if err := crossProcessServe(); err != nil {
		fmt.Fprintln(os.Stderr, "exit serving process:", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func crossProcessServe() error {
	// fork+exec dup3's the parent's ExtraFiles to fd 3 (uffd) and fd 4 (rpc).
	uffdFile := os.NewFile(uintptr(3), "uffd")
	defer uffdFile.Close()

	rpcFile := os.NewFile(uintptr(4), "rpc")
	conn, err := net.FileConn(rpcFile)
	rpcFile.Close()
	if err != nil {
		return fmt.Errorf("net.FileConn rpc: %w", err)
	}
	// Explicit close before <-codecDone unblocks ServeCodec on the success
	// path; the deferred close is the safety net for early returns.
	var closeConnOnce sync.Once
	closeConn := func() { closeConnOnce.Do(func() { _ = conn.Close() }) }
	defer closeConn()

	state := newHarnessState(uffdFile.Fd())

	server := rpc.NewServer()
	if err := server.Register(&Lifecycle{state: state}); err != nil {
		return fmt.Errorf("rpc Register Lifecycle: %w", err)
	}
	if err := server.Register(&Paging{state: state}); err != nil {
		return fmt.Errorf("rpc Register Paging: %w", err)
	}
	if err := server.Register(&Barriers{state: state}); err != nil {
		return fmt.Errorf("rpc Register Barriers: %w", err)
	}

	// Run the codec in a goroutine so Shutdown can unblock us via ctx.
	codecDone := make(chan struct{})
	go func() {
		defer close(codecDone)
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	}()

	select {
	case <-state.ctx.Done():
	case <-codecDone:
	}

	state.mu.Lock()
	br := state.br
	state.mu.Unlock()
	if br != nil {
		br.ReleaseAll()
	}
	state.stopServe()

	closeConn()
	<-codecDone

	return nil
}
