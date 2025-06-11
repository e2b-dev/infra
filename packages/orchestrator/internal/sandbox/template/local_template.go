package template

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type LocalTemplate struct {
	files *storage.TemplateCacheFiles

	memfile block.ReadonlyDevice
	rootfs  block.ReadonlyDevice
}

func NewLocalTemplate(
	files *storage.TemplateCacheFiles,
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

func (t *LocalTemplate) Files() *storage.TemplateCacheFiles {
	return t.files
}

func (t *LocalTemplate) Memfile() (block.ReadonlyDevice, error) {
	return t.memfile, nil
}

func (t *LocalTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs, nil
}

func (t *LocalTemplate) Snapfile() (File, error) {
	return &NoopSnapfile{}, nil
}

func (t *LocalTemplate) ReplaceMemfile(memfile block.ReadonlyDevice) error {
	t.memfile = memfile
	return nil
}

type NoopSnapfile struct{}

func (n *NoopSnapfile) Close() error {
	return nil
}

func (n *NoopSnapfile) Path() string {
	return "/dev/null"
}
