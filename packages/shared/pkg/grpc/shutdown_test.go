package grpc

import (
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
)

// TestGracefulStopWithTimeout_Clean ensures the helper returns true when the
// server has no in-flight RPCs and GracefulStop completes immediately.
func TestGracefulStopWithTimeout_Clean(t *testing.T) {
	t.Parallel()

	srv := grpc.NewServer()
	listenCfg := &net.ListenConfig{}
	ln, err := listenCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()

	start := time.Now()
	ok := GracefulStopWithTimeout(srv, time.Second)
	if !ok {
		t.Fatal("expected clean stop")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("stop took too long: %s", elapsed)
	}
}

// TestGracefulStopWithTimeout_AlreadyStopped is a sanity check that calling
// the helper on an already-stopped server doesn't hang.
func TestGracefulStopWithTimeout_AlreadyStopped(t *testing.T) {
	t.Parallel()

	srv := grpc.NewServer()
	listenCfg := &net.ListenConfig{}
	ln, err := listenCfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()

	if !GracefulStopWithTimeout(srv, time.Second) {
		t.Fatal("first stop should be clean")
	}
	// Second invocation: GracefulStop on a stopped server is a no-op.
	if !GracefulStopWithTimeout(srv, time.Second) {
		t.Fatal("second stop should also report clean")
	}
}
