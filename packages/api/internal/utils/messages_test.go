package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxSnapshotNotFoundMsg(t *testing.T) {
	t.Parallel()

	require.Equal(t, `Snapshot for sandbox "sbx_123" was not found`, SandboxSnapshotNotFoundMsg("sbx_123"))
}
