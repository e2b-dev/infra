package source

import (
	"context"
	"testing"

	"cloud.google.com/go/storage"

	"github.com/stretchr/testify/assert"
)

// This test depends on specific GCS bucket, filepath, and file content.
func TestGCS(t *testing.T) {
	ctx := context.Background()
	bucket := "test-fc-mount"
	filepath := "test1"

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		t.Fatalf("failed to create GCS client: %v", err)
	}
	defer client.Close()

	// Create a new GCS source
	gcs := NewGCSObject(ctx, client, bucket, filepath)

	// Test ReadAt method
	blockSize := int64(4096)
	b := make([]byte, 30*blockSize)
	_, err = gcs.ReadAt(b, 0)

	// Test size method
	size, err := gcs.Size()
	assert.NoError(t, err)
	assert.NotEmpty(t, size)

	assert.NoError(t, err)
	assert.NotEmpty(t, b)
}
