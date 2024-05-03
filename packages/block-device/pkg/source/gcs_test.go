package source

import (
	"context"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This test depends on specific GCS bucket, filepath, and file content.
func TestGCS(t *testing.T) {
	ctx := context.Background()
	bucket := "test-fc-mount"
	filepath := "test1"

	// Create a new GCS source
	gcs, err := NewGCS(ctx, bucket, filepath)
	require.NoError(t, err)
	defer gcs.Close()

	// Test ReadAt method
	b := make([]byte, 2*block.Size)
	n, err := gcs.ReadAt(b, 0)

	assert.NoError(t, err)
	assert.Equal(t, len(b), n)
	assert.NotEmpty(t, b)

	// Test Close method
	err = gcs.Close()
	assert.NoError(t, err)
}
