package nbd

import (
	"context"
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/stretchr/testify/require"
)

func TestNbd(t *testing.T) {
	ctx := context.Background()

	content := "Hello, World!"

	device := block.NewMockDevice([]byte(content), 4096, true)

	nbd, err := NewNbd(ctx, device)
	require.NoError(t, err)

	err = nbd.Stop(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(nbd.Path)
	require.NoError(t, err)

	require.Equal(t, content, string(data))
}
