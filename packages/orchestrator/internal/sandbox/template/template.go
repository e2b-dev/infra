package template

import (
	"errors"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Template interface {
	Files() *storage.TemplateCacheFiles
	Memfile() (*Storage, error)
	Rootfs() (*Storage, error)
	Snapfile() (File, error)
	Close() error
}

func closeTemplate(t Template) error {
	var errs []error

	snapfile, err := t.Snapfile()
	if err == nil {
		errs = append(errs, snapfile.Close())
	}

	return errors.Join(errs...)
}
