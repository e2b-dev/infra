package handler

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandler(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping benchmark because not running as root")
	}

	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "out.txt")

	cancel := func() {}
	currentUser, err := user.Current()
	require.NoError(t, err)

	req := &rpc.StartRequest{
		Process: &rpc.ProcessConfig{
			Cmd:  "touch",
			Args: []string{testFile},
		},
	}
	var output bytes.Buffer
	logger := zerolog.New(zerolog.ConsoleWriter{Out: &output, NoColor: true})
	defaults := &execcontext.Defaults{}

	h, err := New(t.Context(), currentUser, req, &logger, defaults, cancel)
	require.NoError(t, err)

	pid, err := h.Start()
	require.NoError(t, err)
	assert.NotZero(t, pid)

	h.Wait()

	info, err := os.Stat(testFile)
	require.NoError(t, err, output.String())
	assert.Equal(t, int64(0), info.Size())
}
