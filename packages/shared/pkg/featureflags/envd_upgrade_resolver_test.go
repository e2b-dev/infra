package featureflags

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveEnvdUpgradePath exercises the resume-time upgrade decision without a
// LaunchDarkly client: the flag value is passed directly, binaries are real temp
// files (for the os.Stat check), and version resolution is injected.
func TestResolveEnvdUpgradePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promoted := filepath.Join(dir, "envd")       // HOST_ENVD_PATH
	shaBin := filepath.Join(dir, "envd.1f95888") // versioned binary
	require.NoError(t, os.WriteFile(promoted, []byte("x"), 0o755))
	require.NoError(t, os.WriteFile(shaBin, []byte("x"), 0o755))

	// Version map: promoted binary is 0.6.12, the SHA binary is 0.7.0. An
	// unknown path returns an error (unreadable binary).
	versions := map[string]string{promoted: "0.6.12", shaBin: "0.7.0"}
	getVersion := func(_ context.Context, path string) (string, error) {
		v, ok := versions[path]
		if !ok {
			return "", fmt.Errorf("unknown binary %s", path)
		}

		return v, nil
	}

	tests := []struct {
		name        string
		target      string // flag value
		builtWith   string
		wantPath    string
		wantVersion string
		wantReason  string
	}{
		{"off returns empty", "off", "0.6.11", "", "", "off"},
		{"empty (unset) returns empty", "", "0.6.11", "", "", "off"},
		{"promoted, newer -> promoted path", "promoted", "0.6.11", promoted, "0.6.12", ""},
		{"promoted, same version -> empty (idempotent)", "promoted", "0.6.12", "", "", "same_version"},
		{"sha, exists and newer -> sha path", "1f95888", "0.6.11", shaBin, "0.7.0", ""},
		{"sha, same version -> empty", "1f95888", "0.7.0", "", "", "same_version"},
		{"sha, missing binary -> not_staged", "deadbee", "0.6.11", "", "", "not_staged"},
		// Upgrade-only: an older staged target must not trigger a downgrade.
		{"sha, older target -> downgrade refused", "1f95888", "0.8.0", "", "", "downgrade"},
		{"promoted, older target -> downgrade refused", "promoted", "0.7.0", "", "", "downgrade"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotPath, gotVersion, gotReason := resolveEnvdUpgradePath(t.Context(), tt.target, tt.builtWith, promoted, getVersion)
			assert.Equal(t, tt.wantPath, gotPath)
			assert.Equal(t, tt.wantVersion, gotVersion)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

// TestResolveEnvdUpgradePath_VersionError verifies an unreadable target is
// treated as "no upgrade" rather than propagating an error into the resume path.
func TestResolveEnvdUpgradePath_VersionError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	promoted := filepath.Join(dir, "envd")
	require.NoError(t, os.WriteFile(promoted, []byte("x"), 0o755))

	getVersion := func(_ context.Context, _ string) (string, error) {
		return "", assert.AnError
	}

	gotPath, gotVersion, gotReason := resolveEnvdUpgradePath(t.Context(), "promoted", "0.6.11", promoted, getVersion)
	assert.Empty(t, gotPath)
	assert.Empty(t, gotVersion)
	assert.Equal(t, "getversion_failed", gotReason)
}
