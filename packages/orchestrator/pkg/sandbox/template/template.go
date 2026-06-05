//go:build linux

package template

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// Layer identifies a snapshot layer in the three-layer architecture.
type Layer string

const (
	Layer0 Layer = "L0" // infrastructure (kernel, envd, base system)
	Layer1 Layer = "L1" // language/framework runtime
	Layer2 Layer = "L2" // per-instance private state
)

type Template interface {
	Files() storage.CachePaths
	Memfile(ctx context.Context) (block.ReadonlyDevice, error)
	Rootfs() (block.ReadonlyDevice, error)
	Snapfile() (File, error)
	Metadata() (metadata.Template, error)
	UpdateMetadata(meta metadata.Template) error
	Close(ctx context.Context) error

	// Layer returns which layer this template belongs to.
	Layer() Layer

	// ParentTemplate returns the template this one was built from.
	// L1 returns L0, L2 returns L1, L0 returns nil.
	ParentTemplate() Template

	// SharedMemfilePath returns the absolute path to the memfile that
	// should be shared across VMs via SharedMemfileManager.
	// L0 and L1 return their cache memfile path; L2 and non-layered
	// templates return "".
	SharedMemfilePath() string

	// OverlayPath returns the path where the CoW overlay file should be
	// stored for this layer. L2 returns a path based on its cache dir;
	// L0/L1 and non-layered templates return "".
	OverlayPath() string
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
