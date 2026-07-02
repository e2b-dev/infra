package storage

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type partUploaderTestAdapter struct {
	name          string
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

func testPartUploaderContract(t *testing.T, adapters []partUploaderTestAdapter) {
	t.Helper()

	for _, adapter := range adapters {
		t.Run(adapter.name+"/uploads slices and completes in part order", func(t *testing.T) {
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
			continue
		}

		t.Run(adapter.name+"/close aborts unfinished upload", func(t *testing.T) {
			recorder := &partUploaderRecorder{}
			uploader := adapter.new(t, recorder)

			require.NoError(t, uploader.Start(t.Context()))
			require.NoError(t, uploader.Close())

			require.True(t, recorder.started)
			require.False(t, recorder.completed)
			require.True(t, recorder.aborted)
		})
	}
}

func readAllString(t *testing.T, r io.Reader) string {
	t.Helper()

	b, err := io.ReadAll(r)
	require.NoError(t, err)

	return string(b)
}
