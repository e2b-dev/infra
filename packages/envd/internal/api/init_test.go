package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSimpleCases(t *testing.T) {
	testCases := map[string]func(string) string{
		"both newlines":               func(s string) string { return s },
		"no newline prefix":           func(s string) string { return strings.TrimPrefix(s, "\n") },
		"no newline suffix":           func(s string) string { return strings.TrimSuffix(s, "\n") },
		"no newline prefix or suffix": strings.TrimSpace,
	}

	for name, preprocessor := range testCases {
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()

			value := `
# comment
127.0.0.1        one.host
127.0.0.2        two.host
`
			value = preprocessor(value)
			inputPath := filepath.Join(tempDir, "hosts")
			err := os.WriteFile(inputPath, []byte(value), hostsFilePermissions)
			require.NoError(t, err)

			err = rewriteHostsFile("127.0.0.3", inputPath)
			require.NoError(t, err)

			data, err := os.ReadFile(inputPath)
			require.NoError(t, err)

			assert.Equal(t, `# comment
127.0.0.1        one.host
127.0.0.2        two.host
127.0.0.3        events.e2b.local`, strings.TrimSpace(string(data)))
		})
	}
}

func TestShouldSetSystemTime(t *testing.T) {
	sandboxTime := time.Now()

	tests := []struct {
		name     string
		hostTime time.Time
		want     bool
	}{
		{
			name:     "current time far in past compared to sandboxTime (should set)",
			hostTime: sandboxTime.Add(-10 * time.Second),
			want:     true,
		},
		{
			name:     "current time at maxTimeInPast boundary before sandboxTime (should set)",
			hostTime: sandboxTime.Add(-50 * time.Millisecond),
			want:     true,
		},
		{
			name:     "current time just within maxTimeInPast before sandboxTime (should not set)",
			hostTime: sandboxTime.Add(-40 * time.Millisecond),
			want:     false,
		},
		{
			name:     "current time slightly before sandboxTime (should not set)",
			hostTime: sandboxTime.Add(-10 * time.Millisecond),
			want:     false,
		},
		{
			name:     "current time equals sandboxTime (should not set)",
			hostTime: sandboxTime,
			want:     false,
		},
		{
			name:     "current time slightly after sandboxTime (should not set)",
			hostTime: sandboxTime.Add(1 * time.Second),
			want:     false,
		},
		{
			name:     "current time just within maxTimeInFuture after sandboxTime (should not set)",
			hostTime: sandboxTime.Add(4 * time.Second),
			want:     false,
		},
		{
			name:     "current time at maxTimeInFuture boundary after sandboxTime (should set)",
			hostTime: sandboxTime.Add(5 * time.Second),
			want:     true,
		},
		{
			name:     "current time far in future compared to sandboxTime (should set)",
			hostTime: sandboxTime.Add(1 * time.Minute),
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSetSystemTime(tt.hostTime, sandboxTime)
			assert.Equal(t, tt.want, got)
		})
	}
}
