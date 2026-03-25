package cmdutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func isGCSPath(path string) bool {
	return strings.HasPrefix(path, "gs://") || strings.HasPrefix(path, "gs:")
}

func normalizeGCSPath(path string) string {
	if strings.HasPrefix(path, "gs://") {
		return path
	}
	if bucket, found := strings.CutPrefix(path, "gs:"); found {
		return "gs://" + bucket
	}

	return path
}

func extractBucketName(path string) string {
	return strings.TrimPrefix(normalizeGCSPath(path), "gs://")
}

// SetupStorage configures storage environment variables based on the storage path.
// If path starts with "gs://" or "gs:", configures GCS storage.
// Otherwise, configures local storage.
func SetupStorage(storagePath string) {
	absPath := func(p string) string {
		abs, err := filepath.Abs(p)
		if err != nil {
			return p
		}

		return abs
	}

	if isGCSPath(storagePath) {
		os.Setenv("STORAGE_PROVIDER", "GCPBucket")
		os.Setenv("TEMPLATE_BUCKET_NAME", extractBucketName(storagePath))
	} else {
		os.Setenv("STORAGE_PROVIDER", "Local")
		os.Setenv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", absPath(filepath.Join(storagePath, "templates")))
	}
}

func GetProvider(ctx context.Context, storagePath string) (storage.StorageProvider, error) {
	SetupStorage(storagePath)

	return storage.GetStorageProvider(ctx, storage.TemplateStorageConfig)
}
