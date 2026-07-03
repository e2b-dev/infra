package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

//nolint:paralleltest // uses t.Setenv, which is incompatible with t.Parallel
func TestDebugArtifactsBaseURL(t *testing.T) {
	t.Setenv("E2B_GDB_ARTIFACTS_URL", "")
	if got := debugArtifactsBaseURL(); got != defaultDebugArtifactsBaseURL {
		t.Fatalf("default: got %q, want %q", got, defaultDebugArtifactsBaseURL)
	}

	t.Setenv("E2B_GDB_ARTIFACTS_URL", "http://localhost:8077/")
	if got, want := debugArtifactsBaseURL(), "http://localhost:8077"; got != want {
		t.Fatalf("override (trailing slash trimmed): got %q, want %q", got, want)
	}
}

func TestResolveOrFetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// Resolution paths that must NOT fetch use a fast-failing URL, so a regression
	// that wrongly fetches surfaces as an error rather than a silent pass.
	const noFetchURL = "http://127.0.0.1:0/unused"

	t.Run("override returned when present, no fetch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		override := filepath.Join(dir, "override.bin")
		if err := os.WriteFile(override, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveOrFetch(ctx, override, filepath.Join(dir, "local"), noFetchURL, 0o644)
		if err != nil || got != override {
			t.Fatalf("got %q, err %v; want %q", got, err, override)
		}
	})

	t.Run("missing override errors", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if _, err := resolveOrFetch(ctx, filepath.Join(dir, "nope"), filepath.Join(dir, "local"), noFetchURL, 0o644); err == nil {
			t.Fatal("expected error for missing override")
		}
	})

	t.Run("local staged copy returned, no fetch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		local := filepath.Join(dir, "local.bin")
		if err := os.WriteFile(local, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveOrFetch(ctx, "", local, noFetchURL, 0o644)
		if err != nil || got != local {
			t.Fatalf("got %q, err %v; want %q", got, err, local)
		}
	})

	t.Run("fetches when absent and creates parent dir", func(t *testing.T) {
		t.Parallel()
		const body = "fetched-artifact"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/firecracker-debug" {
				w.WriteHeader(http.StatusNotFound)

				return
			}
			_, _ = w.Write([]byte(body))
		}))
		defer srv.Close()

		dir := t.TempDir()
		local := filepath.Join(dir, "sub", "firecracker-debug") // parent does not exist yet
		got, err := resolveOrFetch(ctx, "", local, srv.URL+"/firecracker-debug", 0o755)
		if err != nil {
			t.Fatalf("fetch failed: %v", err)
		}
		if got != local {
			t.Fatalf("got %q, want %q", got, local)
		}
		b, err := os.ReadFile(local)
		if err != nil || string(b) != body {
			t.Fatalf("content %q err %v; want %q", b, err, body)
		}
	})

	t.Run("404 errors", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		dir := t.TempDir()
		if _, err := resolveOrFetch(ctx, "", filepath.Join(dir, "x"), srv.URL+"/missing", 0o644); err == nil {
			t.Fatal("expected 404 error")
		}
	})
}
