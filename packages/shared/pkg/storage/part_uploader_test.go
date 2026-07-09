package storage

import (
	"cmp"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type partUploaderTestAdapter struct {
	abortsOnClose bool
	new           func(t *testing.T, recorder *partUploaderRecorder) partUploader
}

type partUploaderRecorder struct {
	started   bool
	completed bool
	aborted   bool
	parts     []recordedPart
}

type recordedPart struct {
	number int
	body   string
}

func testPartUploaderContract(t *testing.T, adapter partUploaderTestAdapter) {
	t.Helper()

	t.Run("uploads slices and completes in part order", func(t *testing.T) {
		t.Parallel()

		recorder := &partUploaderRecorder{}
		uploader := adapter.new(t, recorder)

		require.NoError(t, uploader.Start(t.Context()))
		require.NoError(t, uploader.UploadPart(t.Context(), 2, []byte("two")))
		require.NoError(t, uploader.UploadPart(t.Context(), 1, []byte("one"), []byte("-split")))
		require.NoError(t, uploader.Complete(t.Context()))
		require.NoError(t, uploader.Close())

		require.True(t, recorder.started)
		require.True(t, recorder.completed)
		require.False(t, recorder.aborted)
		require.Equal(t, []recordedPart{
			{number: 2, body: "two"},
			{number: 1, body: "one-split"},
		}, recorder.parts)
	})

	if !adapter.abortsOnClose {
		return
	}

	t.Run("close aborts unfinished upload", func(t *testing.T) {
		t.Parallel()

		recorder := &partUploaderRecorder{}
		uploader := adapter.new(t, recorder)

		require.NoError(t, uploader.Start(t.Context()))
		require.NoError(t, uploader.Close())

		require.True(t, recorder.started)
		require.False(t, recorder.completed)
		require.True(t, recorder.aborted)
	})
}

// recordUploadedPart handles a multipart PUT in a fake server: it records the
// part number and body and replies with a per-part ETag, echoing the CRC32
// checksum back like S3 does (the body must be read first — the SDK may send
// the checksum as an HTTP trailer).
func recordUploadedPart(t *testing.T, recorder *partUploaderRecorder, w http.ResponseWriter, r *http.Request) {
	t.Helper()

	n, err := strconv.Atoi(r.URL.Query().Get("partNumber"))
	require.NoError(t, err)
	recorder.parts = append(recorder.parts, recordedPart{number: n, body: readAllString(t, r.Body)})
	if c := cmp.Or(r.Header.Get("x-amz-checksum-crc32"), r.Trailer.Get("x-amz-checksum-crc32")); c != "" {
		w.Header().Set("x-amz-checksum-crc32", c)
	}
	w.Header().Set("ETag", fmt.Sprintf(`"etag%d"`, n))
	w.WriteHeader(http.StatusOK)
}

func readAllString(t *testing.T, r io.Reader) string {
	t.Helper()

	b, err := io.ReadAll(r)
	require.NoError(t, err)

	return string(b)
}
