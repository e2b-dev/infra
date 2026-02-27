package peerprovider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// collectSender accumulates all data passed to Send.
type collectSender struct {
	data []byte
}

func (s *collectSender) Send(chunk []byte) error {
	s.data = append(s.data, chunk...)

	return nil
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}
