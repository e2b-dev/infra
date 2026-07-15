//go:build linux

package sandbox

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/rootfs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type DiffCreator interface {
	process(ctx context.Context, out *os.File) (*header.DiffMetadata, error)
}

// RootfsDiffCreator exports the rootfs diff. If closeHook is set, it uses
// the destroy path (ExportDiff stops the sandbox); if nil, it exports it
// in-place and leaves the VM running (calling ExportDiffInPlace).
type RootfsDiffCreator struct {
	rootfs    rootfs.Provider
	closeHook func(context.Context) error
}

func (r *RootfsDiffCreator) process(ctx context.Context, out *os.File) (*header.DiffMetadata, error) {
	if r.closeHook != nil {
		return r.rootfs.ExportDiff(ctx, out, r.closeHook)
	}

	return r.rootfs.ExportDiffInPlace(ctx, out)
}
