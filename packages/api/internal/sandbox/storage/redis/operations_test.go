package redis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxUpdateIsNoop(t *testing.T) {
	t.Parallel()

	require.True(t, sandboxUpdateIsNoop([]byte(`{"sandboxID":"sbx"}`), []byte(`{"sandboxID":"sbx"}`)))
	require.False(t, sandboxUpdateIsNoop([]byte(`{"sandboxID":"sbx"}`), []byte(`{"sandboxID":"sbx","endTime":"later"}`)))
}
