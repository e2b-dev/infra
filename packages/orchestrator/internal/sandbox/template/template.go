package template

import (
	"errors"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Template interface {
	Files() *storage.TemplateCacheFiles
	Memfile() (*block.Storage, error)
	Rootfs() (*block.Storage, error)
	Snapfile() (File, error)
	Close() error
}

func closeTemplate(t Template) error {
	var errs []error

	memfile, err := t.Memfile()
	if err == nil {
		errs = append(errs, memfile.Close())
	}

	rootfs, err := t.Rootfs()
	if err == nil {
		errs = append(errs, rootfs.Close())
	}

	snapfile, err := t.Snapfile()
	if err == nil {
		errs = append(errs, snapfile.Close())
	}

	return errors.Join(errs...)
}
