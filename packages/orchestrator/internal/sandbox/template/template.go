package template

import (
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type NotImplementedError struct {
	Msg string
}

func (e NotImplementedError) Error() string {
	return fmt.Sprintf("not implemented: %s", e.Msg)
}

type Template interface {
	Files() *storage.TemplateCacheFiles
	Memfile() (block.ReadonlyDevice, error)
	Rootfs() (block.ReadonlyDevice, error)
	Snapfile() (File, error)
	Close() error
}

func closeTemplate(t Template) (e error) {
	closable := make([]io.Closer, 0)

	memfile, err := t.Memfile()
	if err != nil {
		e = errors.Join(e, err)
	} else {
		closable = append(closable, memfile)
	}

	rootfs, err := t.Rootfs()
	if err != nil {
		e = errors.Join(e, err)
	} else {
		closable = append(closable, rootfs)
	}

	snapfile, err := t.Snapfile()
	if err != nil {
		var ni *NotImplementedError
		if errors.As(err, &ni) {
			// ignore
		} else {
			e = errors.Join(e, err)
		}
	} else {
		closable = append(closable, snapfile)
	}

	for _, c := range closable {
		if err := c.Close(); err != nil {
			e = errors.Join(e, err)
		}
	}

	if e != nil {
		return fmt.Errorf("error closing template: %w", e)
	}

	return nil
}
