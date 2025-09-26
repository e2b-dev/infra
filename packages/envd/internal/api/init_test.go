package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const perms = 0o700

func TestSimpleCases(t *testing.T) {
	testCases := map[string]string{
		"header newline": `
127.0.0.1           localhost`,
		"trailing newline": `127.0.0.1           localhost
`,
		"trimmed string": "127.0.0.1           localhost",
	}

	for name, value := range testCases {
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()

			inputPath := filepath.Join(tempDir, "hosts")
			err := os.WriteFile(inputPath, []byte(value), perms)
			require.NoError(t, err)

			err = rewriteHostsFile("127.0.0.2", inputPath, inputPath)
			require.NoError(t, err)

			data, err := os.ReadFile(inputPath)
			require.NoError(t, err)

			assert.Equal(t, `127.0.0.1        localhost
127.0.0.2        events.e2b.local
`, string(data))

		})
	}
}
