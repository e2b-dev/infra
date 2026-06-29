//go:build linux

package uffd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// mockMemfile is a minimal ReadonlyDevice for testing.
type mockMemfile struct{}

func (m *mockMemfile) ReadAt(_ context.Context, p []byte, _ int64) (int, error) {
	return len(p), nil
}

func (m *mockMemfile) Size(_ context.Context) (int64, error) {
	return 0, nil
}

func (m *mockMemfile) Close() error {
	return nil
}

func (m *mockMemfile) Slice(_ context.Context, _, _ int64) ([]byte, error) {
	return nil, nil
}

func (m *mockMemfile) BlockSize() int64 {
	return 4096
}

func (m *mockMemfile) Header() *header.Header {
	return nil
}

func (m *mockMemfile) SwapHeader(_ *header.Header) {}

func TestHandleFailureSetsExitError(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	u := New(&mockMemfile{}, socketPath)

	ctx := context.Background()
	if err := u.Start(ctx, "test-sandbox"); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Close the listener to make handle()'s Accept() fail immediately.
	u.lis.Close()

	// Wait for the exit signal with a timeout.
	select {
	case <-u.Exit().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exit signal after handle failure")
	}

	// exit should carry an error because handle() failed.
	exitErr := u.Exit().Wait()
	if exitErr == nil {
		t.Fatal("expected exit error after handle failure, got nil")
	}

	t.Logf("exit error (expected): %v", exitErr)
}

func TestHandleFailureClosesReadyChannel(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	u := New(&mockMemfile{}, socketPath)

	ctx := context.Background()
	if err := u.Start(ctx, "test-sandbox"); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Close the listener to make handle() fail.
	u.lis.Close()

	// readyCh should be closed even on failure, to unblock waiters.
	select {
	case <-u.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for readyCh to close after handle failure")
	}
}

func TestHandleFailureSetsHandlerError(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	u := New(&mockMemfile{}, socketPath)

	ctx := context.Background()
	if err := u.Start(ctx, "test-sandbox"); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Close the listener to make handle() fail.
	u.lis.Close()

	// Wait for exit to ensure the goroutine has finished.
	select {
	case <-u.Exit().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exit")
	}

	// Prefault should fail because handler was never set (SetError was called).
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	_, err := u.Prefault(ctx, 0, []byte{0})
	if err == nil {
		t.Fatal("expected Prefault to fail after handle failure, got nil")
	}

	t.Logf("Prefault error (expected): %v", err)
}

func TestStartFailsOnInvalidSocketPath(t *testing.T) {
	// Use a path that cannot be created.
	socketPath := filepath.Join("/nonexistent-dir-xxx", "test.sock")

	u := New(&mockMemfile{}, socketPath)

	ctx := context.Background()
	err := u.Start(ctx, "test-sandbox")
	if err == nil {
		t.Fatal("expected Start() to fail with invalid socket path")
	}

	t.Logf("Start error (expected): %v", err)
}

func TestNewUffdInitialState(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	u := New(&mockMemfile{}, socketPath)

	// readyCh should not be closed initially.
	select {
	case <-u.Ready():
		t.Fatal("readyCh should not be closed before Start()")
	default:
	}

	// exit should not be done initially.
	select {
	case <-u.Exit().Done():
		t.Fatal("exit should not be done before Start()")
	default:
	}
}

func TestSocketFileCreatedOnStart(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "test.sock")

	u := New(&mockMemfile{}, socketPath)

	ctx := context.Background()
	if err := u.Start(ctx, "test-sandbox"); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Verify socket file exists.
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("socket file not found: %v", err)
	}

	// Verify permissions are 0777.
	perm := info.Mode().Perm()
	if perm != 0o777 {
		t.Errorf("expected socket permissions 0777, got %o", perm)
	}

	// Clean up: close listener to let goroutine exit.
	u.lis.Close()

	select {
	case <-u.Exit().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cleanup")
	}
}
