//go:build linux

package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeBinDir points PATH at a directory whose only entry is a fake `e2fsck`
// script; an empty script means no e2fsck on PATH at all.
func fakeBinDir(t *testing.T, script string) string {
	t.Helper()

	dir := t.TempDir()
	if script != "" {
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "e2fsck"),
			[]byte("#!/bin/sh\n"+script+"\n"),
			0o755,
		))
	}
	t.Setenv("PATH", dir)

	return dir
}

func TestCheckIntegrityExitStatus(t *testing.T) { //nolint:paralleltest // t.Setenv cannot be used with t.Parallel.
	tests := []struct {
		name    string
		script  string
		fix     bool
		wantErr bool
	}{
		{
			name:    "clean filesystem is reported healthy",
			script:  "exit 0",
			fix:     true,
			wantErr: false,
		},
		{
			name:    "uncorrected errors are reported",
			script:  "exit 4",
			fix:     true,
			wantErr: true,
		},
		{
			name:    "operational error is reported",
			script:  "exit 8",
			fix:     true,
			wantErr: true,
		},
		{
			name:    "any non-zero status is reported when not fixing",
			script:  "exit 1",
			fix:     false,
			wantErr: true,
		},
		{
			name:    "corrected errors are accepted when fixing",
			script:  "exit 1",
			fix:     true,
			wantErr: false,
		},
		{
			name:    "corrected errors needing reboot are accepted when fixing",
			script:  "exit 2",
			fix:     true,
			wantErr: false,
		},
		{
			// 3 == e2fsckErrorsCorrected|e2fsckRebootRecommended, both accepted bits at once.
			name:    "combined corrected+reboot bitmask is accepted when fixing",
			script:  "exit 3",
			fix:     true,
			wantErr: false,
		},
		{
			name:    "e2fsck killed by a signal is reported",
			script:  "kill -9 $$",
			fix:     true,
			wantErr: true,
		},
		{
			name:    "e2fsck failing to start is reported",
			script:  "",
			fix:     true,
			wantErr: true,
		},
	}

	for _, tt := range tests { //nolint:paralleltest // t.Setenv cannot be used with t.Parallel.
		t.Run(tt.name, func(t *testing.T) {
			fakeBinDir(t, tt.script)

			_, err := CheckIntegrity(t.Context(), "/tmp/does-not-matter.ext4", tt.fix)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
