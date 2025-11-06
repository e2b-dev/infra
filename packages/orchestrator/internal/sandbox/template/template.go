package template

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/paths"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
)

type Template interface {
	Files() paths.TemplateCacheFiles
	Memfile(ctx context.Context) (block.ReadonlyDevice, error)
	Rootfs() (block.ReadonlyDevice, error)
	Snapfile() (File, error)
	Metadata() (metadata.Template, error)
	Close(ctx context.Context) error
}

func closeTemplate(ctx context.Context, t Template) (e error) {
	closable := make([]io.Closer, 0)

	memfile, err := t.Memfile(ctx)
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
		e = errors.Join(e, err)
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

type NoopFile struct{}

func (n *NoopFile) Close() error {
	return nil
}

func (n *NoopFile) Path() string {
	return "/dev/null"
}
