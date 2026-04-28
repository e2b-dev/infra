package template

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Template interface {
	Files() storage.CachePaths
	Memfile(ctx context.Context) (block.ReadonlyDevice, error)
	Rootfs() (block.ReadonlyDevice, error)
	Snapfile() (File, error)
	Metadata() (metadata.Template, error)
	UpdateMetadata(meta metadata.Template) error
	Close(ctx context.Context) error

	// MemfileFile and RootfsFile return the underlying *build.File for the
	// template's memfile/rootfs, or nil when the template has no such backing
	// (e.g., LocalTemplate for a locally-built base template). Callers that
	// need *build.File-specific APIs (e.g., FinalHeader for upload-time
	// parent-state refresh) use these rather than reaching through the
	// ReadonlyDevice interface.
	MemfileFile() *build.File
	RootfsFile() *build.File
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
