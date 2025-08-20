package template

import (
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type LocalTemplate struct {
	files storage.TemplateCacheFiles

	memfile block.ReadonlyDevice
	rootfs  block.ReadonlyDevice
}

func NewLocalTemplate(
	files storage.TemplateCacheFiles,
	rootfs block.ReadonlyDevice,
	memfile block.ReadonlyDevice,
) *LocalTemplate {
	return &LocalTemplate{
		files:   files,
		memfile: memfile,
		rootfs:  rootfs,
	}
}

func (t *LocalTemplate) Close() error {
	return closeTemplate(t)
}

func (t *LocalTemplate) Files() storage.TemplateCacheFiles {
	return t.files
}

func (t *LocalTemplate) Memfile() (block.ReadonlyDevice, error) {
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

func (t *LocalTemplate) ReplaceMemfile(memfile block.ReadonlyDevice) error {
	t.memfile = memfile
	return nil
}
