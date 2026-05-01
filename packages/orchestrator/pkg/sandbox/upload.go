package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Upload struct {
	id, parent uuid.UUID
	snap       *Snapshot
	paths      storage.Paths
	uploads    *Uploads
	store      storage.StorageProvider
	mem        storage.CompressConfig
	root       storage.CompressConfig
	future     *utils.ErrorOnce
}

func NewUpload(
	ctx context.Context,
	uploads *Uploads,
	snap *Snapshot,
	store storage.StorageProvider,
	cfg storage.CompressConfig,
	ff *featureflags.Client,
	useCase string,
) (*Upload, error) {
	u := &Upload{
		id:      snap.BuildID,
		parent:  snap.ParentBuildID,
		snap:    snap,
		paths:   snap.Paths,
		uploads: uploads,
		store:   store,
		mem:     storage.ResolveCompressConfig(ctx, cfg, ff, storage.MemfileName, useCase),
		root:    storage.ResolveCompressConfig(ctx, cfg, ff, storage.RootfsName, useCase),
	}

	if uploads != nil {
		fut, err := uploads.Start(snap.BuildID)
		if err != nil {
			return nil, err
		}
		u.future = fut
	}

	return u, nil
}

func (u *Upload) Run(ctx context.Context) (memHdr, rootHdr []byte, err error) {
	if !u.mem.IsCompressionEnabled() && !u.root.IsCompressionEnabled() {
		return u.runV3(ctx)
	}

	return u.runV4(ctx)
}

// Finish signals the upload's terminal outcome. Safe to call when uploads was
// nil; in that case there's no future to signal.
func (u *Upload) Finish(err error) {
	if u.future != nil {
		_ = u.future.SetError(err)
	}
}

// publish swaps a finalized header into the local cached device so peers and
// Wait()ers see the build as complete. ErrBuildNotInCache is the one acceptable
// failure mode: nothing was cached locally, nothing to swap.
func (u *Upload) publish(ctx context.Context, t build.DiffType, h *headers.Header) error {
	if u.uploads == nil {
		return nil
	}

	dev, err := u.uploads.find(ctx, u.id, t)
	if errors.Is(err, ErrBuildNotInCache) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load %s for swap: %w", t, err)
	}

	dev.SwapHeader(h)

	return nil
}
