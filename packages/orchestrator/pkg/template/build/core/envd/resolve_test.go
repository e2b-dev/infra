package envd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBuildBinary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promoted := filepath.Join(dir, "envd")      // HOST_ENVD_PATH
	flat := filepath.Join(dir, "envd.v0.7.0")   // flat versioned sibling
	nested := filepath.Join(dir, "v0.7.1/envd") // release-bucket layout
	rc := filepath.Join(dir, "envd.v0.8.0-rc1") // flat prerelease sibling
	// The promoted binary is a real executable: the unstaged-target fallback
	// probes its baked version with `envd -version`.
	require.NoError(t, os.WriteFile(promoted, []byte("#!/bin/sh\necho 0.7.2\n"), 0o755))
	require.NoError(t, os.WriteFile(flat, []byte("x"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(nested), 0o755))
	require.NoError(t, os.WriteFile(nested, []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(rc, []byte("x"), 0o755))

	tests := []struct {
		name     string
		target   string // build-envd-version flag value
		wantPath string
		wantErr  bool
	}{
		{"promoted keeps the host path", "promoted", promoted, false},
		{"empty keeps the host path", "", promoted, false},
		{"flat versioned sibling resolves", "v0.7.0", flat, false},
		{"release-bucket layout resolves", "v0.7.1", nested, false},
		{"prerelease sibling resolves", "v0.8.0-rc1", rc, false},
		// Central-mount nodes stage nothing beside the promoted binary: an
		// unstaged target whose version the promoted binary already carries
		// must be a no-op, not an outage (leading "v" normalized).
		{"unstaged target matching promoted resolves to promoted", "v0.7.2", promoted, false},
		{"unstaged target matching promoted, no leading v", "0.7.2", promoted, false},
		// A pinned build must fail rather than silently bake a different envd.
		{"unstaged version errors", "v9.9.9", "", true},
		{"unstaged prerelease errors", "v0.9.0-rc1", "", true},
		// The value is joined into a filesystem path: reject anything that
		// could traverse out of the staging directory.
		{"path traversal rejected", "../../etc/passwd", "", true},
		{"slash rejected", "sub/envd", "", true},
		{"leading dot rejected", ".hidden", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotPath, err := ResolveBuildBinary(t.Context(), tt.target, promoted)
			if tt.wantErr {
				require.Error(t, err)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, gotPath)
		})
	}
}
