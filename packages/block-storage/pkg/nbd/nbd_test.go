package nbd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/stretchr/testify/require"
)

func TestNbdRead(t *testing.T) {
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

func TestNdbReference(t *testing.T) {
	content := "Hello, World!"

	device := block.NewMockDevice([]byte(content), 4096, true)

	name := filepath.Join(os.TempDir(), "nbd_test")

	f, err := os.Create(name)
	require.NoError(t, err)

	defer f.Close()
	defer os.Remove(name)

	for i := 0; i < len(content)/4096+1; i++ {
		b := make([]byte, 4096)

		off := int64(i * 4096)

		readN, writeErr := device.ReadAt(b, off)
		require.NoError(t, writeErr)

		_, writeErr = f.WriteAt(b[:readN], off)
		require.NoError(t, writeErr)
	}

	data, err := os.ReadFile(name)
	require.NoError(t, err)

	require.Equal(t, content, string(data))
}
