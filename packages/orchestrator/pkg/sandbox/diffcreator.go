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

type RootfsDiffCreator struct {
	rootfs    rootfs.Provider
	closeHook func(context.Context) error
	// recoverJournal runs a host-side ext4 journal recovery before the export
	// (filesystem-only pause on a guest without FIFREEZE support).
	recoverJournal bool
}

func (r *RootfsDiffCreator) process(ctx context.Context, out *os.File) (*header.DiffMetadata, error) {
	return r.rootfs.ExportDiff(ctx, out, r.closeHook, r.recoverJournal)
}
