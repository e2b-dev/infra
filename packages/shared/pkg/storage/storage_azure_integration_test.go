package storage

// Integration test for the Azure Blob storage backend. It runs against any
// real blob endpoint reachable through AZURE_STORAGE_CONNECTION_STRING —
// typically Azurite, the official Azure Storage emulator:
//
//	npx --yes --package azurite azurite-blob --location /tmp/azurite --blobPort 10000
//	AZURE_STORAGE_CONNECTION_STRING='DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw==;BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;' \
//	  go test -count=1 -run TestAzureIntegration ./pkg/storage/ -v
//
// The test skips itself when the connection string is not set, so it is inert
// in normal CI runs.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzureIntegration(t *testing.T) {
	connectionString := os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	if connectionString == "" {
		t.Skip("AZURE_STORAGE_CONNECTION_STRING is not set; skipping Azure Blob integration test")
	}

	ctx := t.Context()

	// Container names must be 3-63 lowercase alphanumeric/hyphen characters.
	containerName := fmt.Sprintf("e2b-it-%d", time.Now().UnixNano())

	// Create (and later remove) the container with a raw SDK client; the
	// provider under test assumes the container already exists.
	adminClient, err := azblob.NewClientFromConnectionString(connectionString, nil)
	require.NoError(t, err)

	_, err = adminClient.CreateContainer(ctx, containerName, nil)
	require.NoError(t, err, "failed to create test container %q", containerName)
	t.Cleanup(func() {
		_, err := adminClient.DeleteContainer(context.Background(), containerName, nil)
		if err != nil && !bloberror.HasCode(err, bloberror.ContainerNotFound) {
			t.Logf("failed to delete test container %q: %v", containerName, err)
		}
	})

	// Build the provider through the public entry point.
	t.Setenv("STORAGE_PROVIDER", string(AzureStorageProvider))
	provider, err := GetStorageProvider(ctx, StorageConfig{
		GetBucketName: func() string { return containerName },
	})
	require.NoError(t, err)
	require.IsType(t, (*azureStorage)(nil), provider)

	t.Run("PutExistsWriteToRoundTrip", func(t *testing.T) {
		blob, err := provider.OpenBlob(ctx, "round-trip/blob.bin")
		require.NoError(t, err)

		exists, err := blob.Exists(ctx)
		require.NoError(t, err)
		assert.False(t, exists, "blob must not exist before Put")

		content := []byte("hello from the azure integration test \x00\x01\x02")
		require.NoError(t, blob.Put(ctx, content))

		exists, err = blob.Exists(ctx)
		require.NoError(t, err)
		assert.True(t, exists, "blob must exist after Put")

		var buf bytes.Buffer
		n, err := blob.WriteTo(ctx, &buf)
		require.NoError(t, err)
		assert.Equal(t, int64(len(content)), n)
		assert.Equal(t, content, buf.Bytes())
	})

	t.Run("MetadataRoundTrip", func(t *testing.T) {
		blob, err := provider.OpenBlob(ctx, "metadata/blob.bin")
		require.NoError(t, err)

		// ObjectMetadataSoftDeleted ("storage-index-soft-deleted") exercises
		// the hyphen -> "__" encoding against a real server, which also
		// case-normalizes metadata keys. ObjectMetadataTeamID ("team_id")
		// checks that a single pre-existing underscore survives decoding.
		metadata := ObjectMetadata{
			ObjectMetadataSoftDeleted: "reclaim:action-123",
			ObjectMetadataTeamID:      "team-42",
		}
		require.NoError(t, blob.Put(ctx, []byte("with metadata"), WithMetadata(metadata)))

		got, err := BlobCustomMetadata(ctx, blob)
		require.NoError(t, err)
		assert.Equal(t, metadata, got)
	})

	t.Run("StoreFileRangeReads", func(t *testing.T) {
		// 25 MB > 2x the 10 MB azureUploadBlockSize, forcing a staged
		// multi-block upload (Put Block + Put Block List) rather than a single
		// Put Blob, and giving us two block boundaries to read across.
		const fileSize = 25 * 1024 * 1024
		require.Greater(t, fileSize, 2*azureUploadBlockSize, "file must span multiple upload blocks")

		content := make([]byte, fileSize)
		_, err := rand.New(rand.NewSource(42)).Read(content)
		require.NoError(t, err)

		localPath := filepath.Join(t.TempDir(), "seekable.bin")
		require.NoError(t, os.WriteFile(localPath, content, 0o644))

		seekable, err := provider.OpenSeekable(ctx, "seekable/blob.bin")
		require.NoError(t, err)

		_, _, err = seekable.StoreFile(ctx, localPath)
		require.NoError(t, err)

		size, err := seekable.Size(ctx)
		require.NoError(t, err)
		require.Equal(t, int64(fileSize), size)

		ranges := []struct {
			name string
			off  int64
			len  int64
		}{
			{name: "first byte", off: 0, len: 1},
			{name: "last byte", off: fileSize - 1, len: 1},
			{name: "spanning first block boundary", off: azureUploadBlockSize - 64, len: 128},
			{name: "spanning second block boundary", off: 2*azureUploadBlockSize - 1000, len: 2000},
			{name: "mid-block chunk", off: 5 * 1024 * 1024, len: 256 * 1024},
		}

		for _, r := range ranges {
			t.Run(r.name, func(t *testing.T) {
				reader, source, err := seekable.OpenRangeReader(ctx, r.off, r.len, nil)
				require.NoError(t, err)
				assert.Equal(t, SourceAzure, source)

				got, err := io.ReadAll(reader)
				require.NoError(t, err)

				_, err = reader.Close(ctx)
				require.NoError(t, err)

				assert.Equal(t, content[r.off:r.off+r.len], got, "range %d+%d must match the source file", r.off, r.len)
			})
		}
	})

	t.Run("RangeReaderZeroLength", func(t *testing.T) {
		// In azblob a Count of 0 means CountToEnd; the backend must reject it
		// instead of silently returning the whole blob tail.
		seekable, err := provider.OpenSeekable(ctx, "seekable/blob.bin")
		require.NoError(t, err)

		reader, _, err := seekable.OpenRangeReader(ctx, 0, 0, nil)
		require.ErrorContains(t, err, "invalid range length")
		assert.Nil(t, reader)
	})

	t.Run("DeleteObjectsWithPrefix", func(t *testing.T) {
		putBlob := func(path string) {
			t.Helper()
			blob, err := provider.OpenBlob(ctx, path)
			require.NoError(t, err)
			require.NoError(t, blob.Put(ctx, []byte("delete-me")))
		}

		var doomed, kept []string
		for i := range 7 {
			doomed = append(doomed, fmt.Sprintf("prefix-del/obj-%d", i))
		}
		for i := range 2 {
			kept = append(kept, fmt.Sprintf("prefix-keep/obj-%d", i))
		}
		for _, path := range append(append([]string{}, doomed...), kept...) {
			putBlob(path)
		}

		require.NoError(t, provider.DeleteObjectsWithPrefix(ctx, "prefix-del/"))

		for _, path := range doomed {
			blob, err := provider.OpenBlob(ctx, path)
			require.NoError(t, err)
			exists, err := blob.Exists(ctx)
			require.NoError(t, err)
			assert.False(t, exists, "blob %q must be deleted", path)
		}
		for _, path := range kept {
			blob, err := provider.OpenBlob(ctx, path)
			require.NoError(t, err)
			exists, err := blob.Exists(ctx)
			require.NoError(t, err)
			assert.True(t, exists, "sibling blob %q must survive", path)
		}

		// An empty prefix would delete the entire container; it must error out.
		require.ErrorContains(t, provider.DeleteObjectsWithPrefix(ctx, ""), "empty prefix")
	})

	t.Run("UploadSignedURL", func(t *testing.T) {
		signedPut := func(url string, body []byte, withBlobTypeHeader bool) *http.Response {
			t.Helper()

			req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
			require.NoError(t, err)
			if withBlobTypeHeader {
				// Azure's Put Blob requires this header on SAS uploads;
				// S3/GCS-style presigned PUTs have no equivalent, so every
				// client of UploadSignedURL must send it explicitly.
				req.Header.Set("x-ms-blob-type", "BlockBlob")
			}

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			t.Cleanup(func() { resp.Body.Close() })

			return resp
		}

		content := []byte("uploaded through a SAS URL")

		url, err := provider.UploadSignedURL(ctx, "signed/upload.bin", time.Hour)
		require.NoError(t, err)

		resp := signedPut(url, content, true)
		respBody, _ := io.ReadAll(resp.Body)
		require.Equal(t, http.StatusCreated, resp.StatusCode, "signed PUT with x-ms-blob-type must succeed, body: %s", respBody)

		blob, err := provider.OpenBlob(ctx, "signed/upload.bin")
		require.NoError(t, err)
		var buf bytes.Buffer
		_, err = blob.WriteTo(ctx, &buf)
		require.NoError(t, err)
		assert.Equal(t, content, buf.Bytes())

		// Without the header the service rejects the upload
		// (MissingRequiredHeader) — documents the client contract.
		url2, err := provider.UploadSignedURL(ctx, "signed/upload-no-header.bin", time.Hour)
		require.NoError(t, err)

		resp2 := signedPut(url2, content, false)
		assert.GreaterOrEqual(t, resp2.StatusCode, http.StatusBadRequest, "signed PUT without x-ms-blob-type must be rejected")
	})

	t.Run("DeleteNonexistentIsIdempotent", func(t *testing.T) {
		object, err := provider.OpenSeekable(ctx, "never/created.bin")
		require.NoError(t, err)

		deleter, ok := object.(interface{ Delete(ctx context.Context) error })
		require.True(t, ok, "azure seekable must support Delete")
		assert.NoError(t, deleter.Delete(ctx), "deleting a missing blob must succeed (S3 DeleteObject semantics)")
	})
}
