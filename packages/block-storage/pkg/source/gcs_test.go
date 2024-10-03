package source

// import (
// 	"bytes"
// 	"context"
// 	"io"
// 	"os"
// 	"testing"

// 	"cloud.google.com/go/storage"

// 	"github.com/stretchr/testify/assert"
// 	"github.com/stretchr/testify/require"
// )

// // This test depends on specific GCS bucket, filepath, and file content.
// func TestGCS(t *testing.T) {
// 	ctx := context.Background()
// 	bucket := "test-fc-mount"
// 	filepath := "test1"

// 	client, err := storage.NewClient(ctx, storage.WithJSONReads())
// 	if err != nil {
// 		t.Fatalf("failed to create GCS client: %v", err)
// 	}
// 	defer client.Close()

// 	// Create a new GCS source
// 	gcs := NewGCSObject(ctx, client, bucket, filepath)

// 	// Test ReadAt method
// 	blockSize := int64(4096)
// 	b := make([]byte, 30*blockSize)
// 	_, err = gcs.ReadAt(b, 0)

// 	// Test size method
// 	size, err := gcs.Size()
// 	assert.NoError(t, err)
// 	assert.NotEmpty(t, size)

// 	assert.NoError(t, err)
// 	assert.NotEmpty(t, b)
// }

// func TestGCSMemfileUpload(t *testing.T) {
// 	ctx := context.Background()
// 	bucket := "e2b-dev-envs-docker-context"
// 	filepath := "zli4m3wxr03ma3i99w8h/212c1098-9bb5-442d-9460-387e3da47688/memfile"

// 	client, err := storage.NewClient(ctx, storage.WithJSONReads())
// 	if err != nil {
// 		t.Fatalf("failed to create GCS client: %v", err)
// 	}
// 	defer client.Close()

// 	gcs := NewGCSObject(ctx, client, bucket, filepath)

// 	fs, err := os.Open("/mnt/disks/fc-envs/v1/zli4m3wxr03ma3i99w8h/memfile")
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	defer fs.Close()

// 	n, err := gcs.ReadFrom(fs)
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	assert.Equal(t, 4294967296, n)
// }

// func TestGCSMemfileCompositeUpload(t *testing.T) {
// 	ctx := context.Background()
// 	bucket := "e2b-dev-envs-docker-context"
// 	filepath := "zli4m3wxr03ma3i99w8h/212c1098-9bb5-442d-9460-387e3da47688/memfile2"

// 	client, err := storage.NewClient(ctx, storage.WithJSONReads())
// 	if err != nil {
// 		t.Fatalf("failed to create GCS client: %v", err)
// 	}
// 	defer client.Close()

// 	gcs := NewGCSObject(ctx, client, bucket, filepath)

// 	err = gcs.UploadWithCli(
// 		ctx,
// 		"/orchestrator/cache/template/0x5brrleaeg0pxeon4uh/9dc30023-c2e5-4cb7-8f4d-5ae196627abd/memfile",
// 	)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// }

// func TestGCSMemfile(t *testing.T) {
// 	ctx := context.Background()
// 	bucket := "e2b-dev-envs-docker-context"
// 	filepath := "zli4m3wxr03ma3i99w8h/212c1098-9bb5-442d-9460-387e3da47688/memfile"

// 	client, err := storage.NewClient(ctx, storage.WithJSONReads())
// 	if err != nil {
// 		t.Fatalf("failed to create GCS client: %v", err)
// 	}
// 	defer client.Close()

// 	// Create a new GCS source
// 	gcs := NewGCSObject(ctx, client, bucket, filepath)

// 	fs, err := os.Open("/mnt/disks/fc-envs/v1/zli4m3wxr03ma3i99w8h/memfile")
// 	if err != nil {
// 		t.Fatal(err)
// 	}

// 	defer fs.Close()

// 	chunkSize := 2 << 20

// 	local := make([]byte, chunkSize)
// 	storage := make([]byte, chunkSize)
// 	storageOffset := int64(0)

// 	for {
// 		nLocal, err1 := fs.Read(local)
// 		nStorage, err2 := gcs.ReadAt(storage, storageOffset)

// 		storageOffset += int64(chunkSize)

// 		if err1 != nil || err2 != nil {
// 			if err1 == io.EOF && err2 == io.EOF {
// 				return
// 			} else if err1 == io.EOF || err2 == io.EOF {
// 				t.Fatalf("EOF: err1=%v, err2=%v", err1, err2)
// 			} else {
// 				t.Fatal(err1, err2)
// 			}
// 		}

// 		require.Equal(t, nLocal, nStorage, "local (%d) and storage (%d) data size are not equal", nLocal, nStorage)

// 		require.True(t, bytes.Equal(local, storage), "local and storage data are not equal")
// 	}
// }
