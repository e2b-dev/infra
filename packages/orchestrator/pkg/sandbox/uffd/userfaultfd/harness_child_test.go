package userfaultfd

// Child side of the cross-process UFFD test harness. The child is a
// re-execed copy of the test binary entered through
// TestHelperServingProcess; it adopts the inherited rpc socket fd
// (fd 4, the parent's socketpair half), registers the three RPC
// services that share a single *harnessState container, and serves
// JSON-RPC until the parent issues Lifecycle.Shutdown. All actual
// work (build mapping, construct *Userfaultfd, install hooks, run
// Serve) is driven by Lifecycle.Bootstrap rather than env vars or
// extra fds.

import (
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"testing"
)

// TestHelperServingProcess is the entry point for the child helper
// process spawned by configureCrossProcessTest. The parent re-execs
// the test binary with envHelperFlag=1 and the uffd / rpc fds in
// ExtraFiles; this test hands off to crossProcessServe and exits
// with its result.
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

// crossProcessServe wires up the child side: adopt the inherited
// rpc socket fd, register the three RPC services that share a single
// harnessState, and run jsonrpc.ServeCodec until the parent shuts
// us down.
func crossProcessServe() error {
	// The parent handed us two fds via cmd.ExtraFiles; the child-side
	// dup3 inside fork+exec lands them at fd 3 (uffd) and fd 4 (rpc
	// socketpair half) with CLOEXEC cleared automatically.
	uffdFile := os.NewFile(uintptr(3), "uffd")
	defer uffdFile.Close()

	rpcFile := os.NewFile(uintptr(4), "rpc")
	conn, err := net.FileConn(rpcFile)
	// FileConn dups the underlying fd; close rpcFile in both the
	// success and error paths so we don't leak an fd.
	rpcFile.Close()
	if err != nil {
		return fmt.Errorf("net.FileConn rpc: %w", err)
	}
	defer conn.Close()

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

	// Run the codec in a goroutine so we can react to Shutdown
	// without depending on the codec returning.
	codecDone := make(chan struct{})
	go func() {
		defer close(codecDone)
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	}()

	select {
	case <-state.shutdown:
	case <-codecDone:
	}

	// Release any still-parked barriers so the serve goroutine can
	// finish, then stop the serve goroutine.
	state.releaseAllBarriers()
	state.stopServe()

	// Closing the conn is sufficient to unblock ServeCodec if it
	// hasn't already returned.
	_ = conn.Close()
	<-codecDone

	return nil
}
