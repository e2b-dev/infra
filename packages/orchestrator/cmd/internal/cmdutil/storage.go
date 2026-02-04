package cmdutil

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
)

// SetupStorage configures storage environment variables based on the storage path.
// If path starts with "gs://", configures GCS storage.
// Otherwise, configures local storage.
func SetupStorage(storagePath string) error {
	absPath := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}

		return abs
	}

	if strings.HasPrefix(storagePath, "gs://") {
		os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		os.Setenv("TEMPLATE_BUCKET_NAME", strings.TrimPrefix(storagePath, "gs://"))
	} else {
		os.Setenv("STORAGE_PROVIDER", "Local")
		os.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", absPath(filepath.Join(storagePath, "templates")))
	}

	return nil
}

// ReadFile reads a file from local storage or GCS.
// Returns the file content, source path, and any error.
func ReadFile(ctx context.Context, storagePath, buildID, filename string) ([]byte, string, error) {
	if strings.HasPrefix(storagePath, "gs://") {
		gcsPath := storagePath + "/" + buildID + "/" + filename

		return ReadFromGCS(ctx, gcsPath)
	}

	localPath := filepath.Join(storagePath, "templates", buildID, filename)
	data, err := os.ReadFile(localPath)

	return data, localPath, err
}

// ReadHeader reads a header file from local storage or GCS.
// The headerPath should be relative (e.g., "buildID/memfile.header").
func ReadHeader(ctx context.Context, storagePath, headerPath string) ([]byte, string, error) {
	if strings.HasPrefix(storagePath, "gs://") {
		return ReadFromGCS(ctx, storagePath+"/"+headerPath)
	}

	localPath := filepath.Join(storagePath, "templates", headerPath)
	data, err := os.ReadFile(localPath)

	return data, localPath, err
}

// ReadFromGCS reads a file from GCS.
// The gcsPath should be in the format "gs://bucket/object".
func ReadFromGCS(ctx context.Context, gcsPath string) ([]byte, string, error) {
	path := strings.TrimPrefix(gcsPath, "gs://")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil, "", fmt.Errorf("invalid GCS path: %s", gcsPath)
	}

	bucket, object := parts[0], parts[1]

	client, err := gcsstorage.NewClient(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	reader, err := client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read object: %w", err)
	}

	return data, gcsPath, nil
}

// DataReader provides read-at capability for data files.
type DataReader interface {
	ReadAt(p []byte, off int64) (n int, err error)
	Close() error
}

type localReader struct {
	file *os.File
}

func (r *localReader) ReadAt(p []byte, off int64) (int, error) {
	return r.file.ReadAt(p, off)
}

func (r *localReader) Close() error {
	return r.file.Close()
}

type gcsReader struct {
	client *gcsstorage.Client
	bucket string
	object string
}

func (r *gcsReader) ReadAt(p []byte, off int64) (int, error) {
	ctx := context.Background()
	reader, err := r.client.Bucket(r.bucket).Object(r.object).NewRangeReader(ctx, off, int64(len(p)))
	if err != nil {
		return 0, err
	}
	defer reader.Close()

	return io.ReadFull(reader, p)
}

func (r *gcsReader) Close() error {
	return r.client.Close()
}

// OpenDataFile opens a data file for reading with ReadAt capability.
// Returns a DataReader, file size, source path, and any error.
func OpenDataFile(ctx context.Context, storagePath, buildID, filename string) (DataReader, int64, string, error) {
	if strings.HasPrefix(storagePath, "gs://") {
		gcsPath := storagePath + "/" + buildID + "/" + filename

		return openGCS(ctx, gcsPath)
	}

	localPath := filepath.Join(storagePath, "templates", buildID, filename)

	return openLocal(localPath)
}

func openLocal(path string) (DataReader, int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()

		return nil, 0, "", err
	}

	return &localReader{file: f}, info.Size(), path, nil
}

func openGCS(ctx context.Context, gcsPath string) (DataReader, int64, string, error) {
	path := strings.TrimPrefix(gcsPath, "gs://")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil, 0, "", fmt.Errorf("invalid GCS path: %s", gcsPath)
	}

	bucket, object := parts[0], parts[1]

	client, err := gcsstorage.NewClient(ctx)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to create GCS client: %w", err)
	}

	attrs, err := client.Bucket(bucket).Object(object).Attrs(ctx)
	if err != nil {
		client.Close()

		return nil, 0, "", fmt.Errorf("failed to get object attrs: %w", err)
	}

	return &gcsReader{client: client, bucket: bucket, object: object}, attrs.Size, gcsPath, nil
}
