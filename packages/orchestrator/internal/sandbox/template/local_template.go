package template

import (
	"context"
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

type LocalTemplate struct {
	files paths.TemplateCacheFiles

	memfile block.ReadonlyDevice
	rootfs  block.ReadonlyDevice
}

func NewLocalTemplate(
	files paths.TemplateCacheFiles,
	rootfs block.ReadonlyDevice,
	memfile block.ReadonlyDevice,
) *LocalTemplate {
	return &LocalTemplate{
		files:   files,
		memfile: memfile,
		rootfs:  rootfs,
	}
}

func (t *LocalTemplate) Close(ctx context.Context) error {
	return closeTemplate(ctx, t)
}

func (t *LocalTemplate) Files() paths.TemplateCacheFiles {
	return t.files
}

func (t *LocalTemplate) Memfile(ctx context.Context) (block.ReadonlyDevice, error) {
	_, span := tracer.Start(ctx, "local-template-memfile")
	defer span.End()

	return t.memfile, nil
}

func (t *LocalTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs, nil
}

func (t *LocalTemplate) Snapfile() (File, error) {
	return &NoopFile{}, errors.New("snapfile not available in local template")
}

func (t *LocalTemplate) Metadata() (metadata.Template, error) {
	return metadata.Template{}, errors.New("metadata not available in local template")
}
