package cmdutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// IsGCSPath checks if the path is a GCS path (gs:// or gs:).
func IsGCSPath(path string) bool {
	return strings.HasPrefix(path, "gs://") || strings.HasPrefix(path, "gs:")
}

// NormalizeGCSPath ensures the path has gs:// prefix.
func NormalizeGCSPath(path string) string {
	if strings.HasPrefix(path, "gs://") {
		return path
	}
	if bucket, found := strings.CutPrefix(path, "gs:"); found {
		return "gs://" + bucket
	}

	return path
}

// ExtractBucketName extracts the bucket name from a GCS path.
func ExtractBucketName(path string) string {
	normalized := NormalizeGCSPath(path)

	return strings.TrimPrefix(normalized, "gs://")
}

// SetupStorage configures storage environment variables based on the storage path.
// If path starts with "gs://" or "gs:", configures GCS storage.
// Otherwise, configures local storage.
func SetupStorage(storagePath string) error {
	absPath := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}

		return abs
	}

	if IsGCSPath(storagePath) {
		os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		os.Setenv("TEMPLATE_BUCKET_NAME", ExtractBucketName(storagePath))
	} else {
		os.Setenv("STORAGE_PROVIDER", "Local")
		os.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", absPath(filepath.Join(storagePath, "templates")))
	}

	return nil
}

// ReadFile reads a file from local storage or GCS.
// Returns the file content, source path, and any error.
func ReadFile(ctx context.Context, storagePath, buildID, filename string) ([]byte, string, error) {
	if IsGCSPath(storagePath) {
		gcsPath := NormalizeGCSPath(storagePath) + "/" + buildID + "/" + filename

		return ReadFromGCS(ctx, gcsPath)
	}

	localPath := filepath.Join(storagePath, "templates", buildID, filename)
	data, err := os.ReadFile(localPath)

	return data, localPath, err
}

// ReadHeader reads a header file from local storage or GCS.
// The headerPath should be relative (e.g., "buildID/memfile.header").
func ReadHeader(ctx context.Context, storagePath, headerPath string) ([]byte, string, error) {
	if IsGCSPath(storagePath) {
		return ReadFromGCS(ctx, NormalizeGCSPath(storagePath)+"/"+headerPath)
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
	if IsGCSPath(storagePath) {
		gcsPath := NormalizeGCSPath(storagePath) + "/" + buildID + "/" + filename

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

// ReadFileIfExists reads a file from local storage or GCS.
// Returns nil, "", nil when the file doesn't exist (instead of an error).
func ReadFileIfExists(ctx context.Context, storagePath, buildID, filename string) ([]byte, string, error) {
	data, source, err := ReadFile(ctx, storagePath, buildID, filename)
	if err != nil {
		if isNotFoundError(err) {
			return nil, "", nil
		}

		return nil, "", err
	}

	return data, source, nil
}

// ReadCompressedHeader reads a v4 header file (e.g. "v4.memfile.header.lz4"),
// LZ4-block-decompresses it, and deserializes.
// Returns nil, "", nil when the v4 header doesn't exist.
func ReadCompressedHeader(ctx context.Context, storagePath, buildID, artifactName string) (*header.Header, string, error) {
	filename := storage.V4HeaderName(artifactName)
	data, source, err := ReadFileIfExists(ctx, storagePath, buildID, filename)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read compressed header: %w", err)
	}
	if data == nil {
		return nil, "", nil
	}

	decompressed, err := storage.DecompressLZ4(data, storage.MaxCompressedHeaderSize)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decompress LZ4 header from %s: %w", source, err)
	}

	h, err := header.DeserializeBytes(decompressed)
	if err != nil {
		return nil, "", fmt.Errorf("failed to deserialize compressed header from %s: %w", source, err)
	}

	return h, source, nil
}

// FileInfo contains existence and size information about a file.
type FileInfo struct {
	Name     string
	Path     string
	Exists   bool
	Size     int64
	Metadata map[string]string // GCS custom metadata (nil for local files)
}

// ProbeFile checks if a file exists and returns its info.
func ProbeFile(ctx context.Context, storagePath, buildID, filename string) FileInfo {
	info := FileInfo{Name: filename}

	if IsGCSPath(storagePath) {
		gcsPath := NormalizeGCSPath(storagePath) + "/" + buildID + "/" + filename
		info.Path = gcsPath

		path := strings.TrimPrefix(gcsPath, "gs://")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return info
		}

		client, err := gcsstorage.NewClient(ctx)
		if err != nil {
			return info
		}
		defer client.Close()

		attrs, err := client.Bucket(parts[0]).Object(parts[1]).Attrs(ctx)
		if err != nil {
			return info
		}

		info.Exists = true
		info.Size = attrs.Size
		info.Metadata = attrs.Metadata
	} else {
		localPath := filepath.Join(storagePath, "templates", buildID, filename)
		info.Path = localPath

		fi, err := os.Stat(localPath)
		if err != nil {
			return info
		}

		info.Exists = true
		info.Size = fi.Size()
	}

	return info
}

// isNotFoundError checks if an error indicates a file/object doesn't exist.
func isNotFoundError(err error) bool {
	if os.IsNotExist(err) {
		return true
	}

	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return true
	}

	return false
}

// ListFiles lists all files for a build in storage.
// Returns FileInfo for each file found.
func ListFiles(ctx context.Context, storagePath, buildID string) ([]FileInfo, error) {
	if IsGCSPath(storagePath) {
		return listGCSFiles(ctx, storagePath, buildID)
	}

	return listLocalFiles(storagePath, buildID)
}

func listGCSFiles(ctx context.Context, storagePath, buildID string) ([]FileInfo, error) {
	normalized := NormalizeGCSPath(storagePath)
	bucket := ExtractBucketName(storagePath)
	prefix := buildID + "/"

	client, err := gcsstorage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	defer client.Close()

	var files []FileInfo
	it := client.Bucket(bucket).Objects(ctx, &gcsstorage.Query{Prefix: prefix})

	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		name := strings.TrimPrefix(attrs.Name, prefix)
		files = append(files, FileInfo{
			Name:     name,
			Path:     normalized + "/" + attrs.Name,
			Exists:   true,
			Size:     attrs.Size,
			Metadata: attrs.Metadata,
		})
	}

	return files, nil
}

func listLocalFiles(storagePath, buildID string) ([]FileInfo, error) {
	dir := filepath.Join(storagePath, "templates", buildID)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var files []FileInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fi, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, FileInfo{
			Name:   entry.Name(),
			Path:   filepath.Join(dir, entry.Name()),
			Exists: true,
			Size:   fi.Size(),
		})
	}

	return files, nil
}
