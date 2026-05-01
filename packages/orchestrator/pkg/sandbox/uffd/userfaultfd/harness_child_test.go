package userfaultfd

// Child side of the cross-process UFFD test harness. The child is a
// re-execed copy of the test binary entered through
// TestHelperServingProcess; it dials the parent's rendezvous socket,
// registers the three RPC services that share a single
// *harnessState container, and serves JSON-RPC until the parent
// issues Lifecycle.Shutdown. All actual work (build mapping,
// construct *Userfaultfd, install hooks, run Serve) is driven by
// Lifecycle.Bootstrap rather than env vars or extra fds.

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"testing"
)

// TestHelperServingProcess is the entry point for the child helper
// process spawned by configureCrossProcessTest. The parent re-execs
// the test binary with envHelperFlag=1 and a socket path; this test
// hands off to crossProcessServe and exits with its result.
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

// crossProcessServe wires up the child side: dial the parent socket,
// register the three RPC services that share a single harnessState,
// and run jsonrpc.ServeCodec until the parent shuts us down.
func crossProcessServe() error {
	socketPath := os.Getenv(envSocketPath)
	if socketPath == "" {
		return fmt.Errorf("missing %s", envSocketPath)
	}

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial parent socket: %w", err)
	}
	defer conn.Close()

	// The parent handed us the userfaultfd via cmd.ExtraFiles; the
	// child-side dup3 inside fork+exec lands it at fd 3 with CLOEXEC
	// cleared automatically.
	uffdFile := os.NewFile(uintptr(3), "uffd")
	defer uffdFile.Close()

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
