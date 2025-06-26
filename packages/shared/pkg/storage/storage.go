package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var ErrorObjectNotExist = errors.New("object does not exist")

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
	UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error)
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
	provider := Provider(env.GetEnv(storageProviderEnv, string(DefaultStorageProvider)))

	if provider == LocalStorageProvider {
		basePath := env.GetEnv("LOCAL_TEMPLATE_STORAGE_BASE_PATH", "/tmp/templates")
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
