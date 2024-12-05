package template

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

type localTemplate struct {
	files *storage.TemplateCacheFiles

	snapfile File
	memfile  block.ReadonlyDevice
	rootfs   block.ReadonlyDevice

	templateBuild *storage.TemplateBuild
}

func newLocalTemplate(
	files *storage.TemplateCacheFiles,
	bucket *gcs.BucketHandle,
) (*localTemplate, error) {
	memfile, err := block.NewLocal(files.CacheMemfilePath())
	if err != nil {
		return nil, fmt.Errorf("failed to create memfile: %w", err)
	}

	rootfs, err := block.NewLocal(files.CacheRootfsPath())
	if err != nil {
		return nil, fmt.Errorf("failed to create rootfs: %w", err)
	}

	return &localTemplate{
		files:         files,
		memfile:       memfile,
		rootfs:        rootfs,
		snapfile:      newLocalFile(files.CacheSnapfilePath()),
		templateBuild: storage.NewTemplateBuild(bucket, files.TemplateFiles),
	}, nil
}

func (t *localTemplate) Upload(ctx context.Context) error {
	err := t.templateBuild.Upload(ctx,
		t.files.CacheSnapfilePath(),
		t.files.CacheMemfilePath(),
		t.files.CacheRootfsPath(),
	)
	if err != nil {
		return fmt.Errorf("failed to upload template: %w", err)
	}

	return nil
}

func (t *localTemplate) Close() error {
	return closeTemplate(t)
}

func (t *localTemplate) Files() *storage.TemplateCacheFiles {
	return t.files
}

func (t *localTemplate) Memfile() (block.ReadonlyDevice, error) {
	return t.memfile, nil
}

func (t *localTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs, nil
}

func (t *localTemplate) Snapfile() (File, error) {
	return t.snapfile, nil
}
