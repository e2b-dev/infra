package storage

import (
	"context"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"io"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	ErrorObjectNotExist = errors.New("object does not exist")
)

type Provider string

const (
	GCPStorageProvider   Provider = "GCPBucket"
	AWSStorageProvider   Provider = "AWSBucket"
	LocalStorageProvider Provider = "Local"

	DefaultStorageProvider Provider = GCPStorageProvider

	storageProviderEnv = "STORAGE_PROVIDER"
)

type StorageProvider interface {
	DeleteObjectsWithPrefix(ctx context.Context, prefix string) error
	OpenObject(ctx context.Context, path string) (StorageObjectProvider, error)
	GetDetails() string
}

type StorageObjectProvider interface {
	WriteTo(dst io.Writer) (int64, error)
	WriteFromFileSystem(path string) error

	ReadFrom(src io.Reader) (int64, error)
	ReadAt(buff []byte, off int64) (n int, err error)

	Size() (int64, error)
	Delete() error
}

func GetTemplateStorageProvider(ctx context.Context) (StorageProvider, error) {
	var provider = Provider(os.Getenv(storageProviderEnv))
	if provider == "" {
		provider = DefaultStorageProvider
	}

	if provider == LocalStorageProvider {
		basePath := os.Getenv("TEMPLATE_STORAGE_BASE_PATH")
		if basePath == "" {
			basePath = "/tmp/templates"
			zap.L().Warn(fmt.Sprintf("For local file system, base path is not set. Defaulting to %s", basePath))
		}

		return NewFileSystemStorageProvider(basePath)
	}

	bucketName := utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "Bucket for storing template files")

	// cloud bucket-based storage
	switch provider {
	case AWSStorageProvider:
		return NewAWSBucketStorageProvider(ctx, bucketName)
	case GCPStorageProvider:
		return NewGCPBucketStorageProvider(ctx, bucketName)
	}

	return nil, fmt.Errorf("unknown storage provider: %s", provider)
}
