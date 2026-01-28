package gcs

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs"
)

func TestHandler_Mount(t *testing.T) {
	t.Parallel()

	bucketName := os.Getenv("TEST_GCS_BUCKET_NAME")
	if bucketName == "" {
		t.Skip("TEST_GCS_BUCKET_NAME not set")
	}

	client, err := storage.NewGRPCClient(t.Context(),
		storage.WithDisabledClientMetrics())
	require.NoError(t, err)
	t.Cleanup(func() {
		err = client.Close()
		assert.NoError(t, err)
	})

	bucket := client.Bucket(bucketName)

	t.Run("mounting a subdir implicitly creates a magic directory", func(t *testing.T) {
		t.Parallel()

		subdir := uuid.NewString()

		request := nfs.MountRequest{
			Dirpath: fmt.Appendf(nil, "/%s", subdir),
		}

		h := NewNFSHandler(bucket)
		s, fs, _ := h.Mount(t.Context(), nil, request)
		require.Equal(t, nfs.MountStatusOk, s)
		require.NotNil(t, fs)
		require.NotNil(t, s)

		// root dir
		dir, err := fs.Lstat("")
		require.NoError(t, err)
		require.True(t, dir.IsDir())

		// subdir
		dir, err = fs.Lstat(subdir)
		require.NoError(t, err)
		require.True(t, dir.IsDir())

		fullFilename := strings.Join([]string{subdir, "file.txt"}, "/")

		file, err := fs.Create(fullFilename)
		require.NoError(t, err)
		t.Cleanup(func() {
			err = file.Close()
			assert.NoError(t, err)
		})

		n, err := file.Write([]byte("test"))
		require.NoError(t, err)
		require.Equal(t, 4, n)
		err = file.Close()
		require.NoError(t, err)

		// verify file has been created at the right place in gcs
		obj := bucket.Object(fullFilename)
		attrs, err := obj.Attrs(t.Context())
		require.NoError(t, err)
		require.Equal(t, fullFilename, attrs.Name)
		r, err := obj.NewReader(t.Context())
		require.NoError(t, err)
		data, err := io.ReadAll(r)
		require.NoError(t, err)
		require.Equal(t, "test", string(data))
	})
}
