package storage

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type TemplateBuild interface {
	Remove(ctx context.Context) error
	Upload(ctx context.Context, snapfilePath string, memfilePath *string, rootfsPath *string) chan error
}

func NewTemplateBuild(
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	files *TemplateFiles,
) TemplateBuild {
	switch files.StorageType {
	case LocalStorage:
		return &TemplateLocalBuild{files: files}
	default:
		return &TemplateBucketBuild{
			bucket:        gcs.GetTemplateBucket(),
			memfileHeader: memfileHeader,
			rootfsHeader:  rootfsHeader,
			files:         files,
		}
	}
}
