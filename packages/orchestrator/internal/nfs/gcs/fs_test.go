package gcs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBucketAttrsInverse(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		flag int
		perm os.FileMode
	}{
		{
			name: "basic",
			flag: os.O_RDWR | os.O_CREATE,
			perm: 0o644,
		},
		{
			name: "another",
			flag: os.O_RDONLY,
			perm: 0o755,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			permKey, permVal := fromPermToObjectMetadata(tc.perm)
			metadata := map[string]string{permKey: permVal}
			gotPerm := fromMetadataToPerm(metadata)

			if gotPerm != tc.perm {
				t.Errorf("expected perm %o, got %o", tc.perm, gotPerm)
			}
		})
	}
}

func TestFromBucketAttrs_Empty(t *testing.T) {
	t.Parallel()

	attrs := make(map[string]string)
	gotPerm := fromMetadataToPerm(attrs)

	if gotPerm != 0 {
		t.Errorf("expected perm 0, got %o", gotPerm)
	}
}

type fileLike interface {
	Read(b []byte) (n int, err error)
	Write(b []byte) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
	Close() error
}

type fileFactory struct {
	tempFilename func() string
	openFile     func(name string, flag int, perm os.FileMode) (fileLike, error)
	remove       func(name string) error
}

func TestBucketFS_OpenFile(t *testing.T) {
	t.Parallel()

	bucketName := os.Getenv("TEST_GCS_BUCKET_NAME")
	if bucketName == "" {
		t.Skip("TEST_GCS_BUCKET_NAME not set")
	}

	client, err := storage.NewGRPCClient(t.Context(),
		storage.WithDisabledClientMetrics())
	require.NoError(t, err)

	gcsFS := BucketFS{
		bucket: client.Bucket(bucketName),
	}

	t.Run("O_TRUNC functions like local files", func(t *testing.T) {
		t.Parallel()

		gcsFilename := strings.Join([]string{uuid.NewString(), "test.txt"}, "/")
		localFilename := filepath.Join(t.TempDir(), "test.txt")

		// open the file
		var localFile, gcsFile fileLike
		localFile, err = os.OpenFile(localFilename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
		require.NoError(t, err)
		gcsFile, err = gcsFS.OpenFile(gcsFilename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
		require.NoError(t, err)

		// write some data
		n, err := localFile.Write([]byte("hello world"))
		require.NoError(t, err)
		require.Equal(t, 11, n)

		n, err = gcsFile.Write([]byte("hello world"))
		require.NoError(t, err)
		require.Equal(t, 11, n)

		// close the file
		err = localFile.Close()
		require.NoError(t, err)
		gcsFile.Close()
		require.NoError(t, err)

		// reopen the file, to verify contents
		localFile, err = os.OpenFile(localFilename, os.O_RDONLY, 0o644)
		require.NoError(t, err)
		gcsFile, err = gcsFS.OpenFile(gcsFilename, os.O_RDONLY, 0o644)
		require.NoError(t, err)

		// seek to beginning
		offset, err := localFile.Seek(2, io.SeekStart)
		require.NoError(t, err)
		require.Equal(t, int64(2), offset)

		offset, err = gcsFile.Seek(2, io.SeekStart)
		require.NoError(t, err)
		require.Equal(t, int64(2), offset)

		// read 4 bytes
		buff := make([]byte, 4)
		n, err = localFile.Read(buff)
		require.NoError(t, err)
		require.Equal(t, 4, n)
		require.Equal(t, "llo ", string(buff))

		n, err = gcsFile.Read(buff)
		require.NoError(t, err)
		require.Equal(t, 4, n)
		require.Equal(t, "llo ", string(buff))

		// close the file
		err = localFile.Close()
		require.NoError(t, err)
		err = gcsFile.Close()
		require.NoError(t, err)

		// reopen the file and truncate
		localFile, err = os.OpenFile(localFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		require.NoError(t, err)
		gcsFile, err = gcsFS.OpenFile(gcsFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		require.NoError(t, err)

		// write some data
		n, err = localFile.Write([]byte("bye"))
		require.NoError(t, err)
		require.Equal(t, 3, n)

		n, err = gcsFile.Write([]byte("bye"))
		require.NoError(t, err)
		require.Equal(t, 3, n)

		// close the file
		err = localFile.Close()
		require.NoError(t, err)
		err = gcsFile.Close()
		require.NoError(t, err)

		// reopen the file, to verify contents
		localFile, err = os.OpenFile(localFilename, os.O_RDONLY, 0o644)
		require.NoError(t, err)
		gcsFile, err = gcsFS.OpenFile(gcsFilename, os.O_RDONLY, 0o644)
		require.NoError(t, err)

		// read 4 bytes
		buff = make([]byte, 4)
		n, err = localFile.Read(buff)
		require.NoError(t, err)
		require.Equal(t, 3, n)
		require.Equal(t, "bye", string(buff[:n]))

		buff = make([]byte, 4)
		n, err = gcsFile.Read(buff)
		require.NoError(t, err)
		require.Equal(t, 3, n)
		require.Equal(t, "bye", string(buff[:n]))
	})

	testCases := map[string]fileFactory{
		"local": {
			tempFilename: func() string {
				return filepath.Join(t.TempDir(), uuid.NewString())
			},
			openFile: func(name string, flag int, perm os.FileMode) (fileLike, error) {
				return os.OpenFile(name, flag, perm)
			},
			remove: os.Remove,
		},
		"gcs": {
			tempFilename: func() string {
				return strings.Join([]string{"test-path", uuid.NewString()}, "/")
			},
			openFile: func(name string, flag int, perm os.FileMode) (fileLike, error) {
				return gcsFS.OpenFile(name, flag, perm)
			},
			remove: gcsFS.Remove,
		},
	}

	for name, fileFactory := range testCases {
		t.Run(fmt.Sprintf("writes and overwrites (%s)", name), func(t *testing.T) {
			t.Parallel()

			filename := fileFactory.tempFilename()

			file, err := fileFactory.openFile(filename, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
			require.NoError(t, err)

			t.Cleanup(func() {
				err = fileFactory.remove(filename)
				assert.NoError(t, err)
			})

			n, err := file.Write([]byte("hello world"))
			require.NoError(t, err)
			require.Equal(t, 11, n)

			err = file.Close()
			require.NoError(t, err)

			file, err = fileFactory.openFile(filename, os.O_RDONLY, 0o644)
			require.NoError(t, err)

			buff := make([]byte, 11)
			n, err = file.Read(buff)
			require.NoError(t, err)
			require.Equal(t, 11, n)
			require.Equal(t, "hello world", string(buff))
		})
	}
}
