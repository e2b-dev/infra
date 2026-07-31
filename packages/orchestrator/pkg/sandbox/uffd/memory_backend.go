//go:build linux

package uffd

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type MemoryBackend interface {
	DiffMetadata(ctx context.Context, f *fc.Process) (*header.DiffMetadata, error)
	PrefetchData(ctx context.Context) (block.PrefetchData, error)
	// Prefault returns whether this call installed the page (false on
	// skipped/present/deferred nil-error paths); see Userfaultfd.Prefault.
	Prefault(ctx context.Context, offset int64, data []byte) (installed bool, e error)
	Start(ctx context.Context, sandboxId string) error
	Stop() error
	Ready() chan struct{}
	Exit() *utils.ErrorOnce
	Memfd(ctx context.Context) *block.Memfd
	// PeekMemfd returns the memfd without transferring ownership, so an in-place
	// snapshot can copy the guest's dirty pages while the running VM keeps using
	// it. Unlike Memfd it does not consume the fd.
	PeekMemfd(ctx context.Context) *block.Memfd
	// ServeStats returns a cumulative snapshot of demand faults served so far.
	// Sampled at the envd-init boundary it yields the pages/bytes a guest
	// needed to start.
	ServeStats() userfaultfd.ServeSnapshot
}
