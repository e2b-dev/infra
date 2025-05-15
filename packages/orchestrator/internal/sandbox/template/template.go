package template

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Template interface {
	Files() *storage.TemplateCacheFiles
	Memfile() (block.ReadonlyDevice, error)
	Rootfs() (block.ReadonlyDevice, error)
	Snapfile() (File, error)
	Close() error
}

func closeTemplate(t Template) error {
	snapfile, err := t.Snapfile()
	if err != nil {
		return err
	}
	return snapfile.Close()
}
