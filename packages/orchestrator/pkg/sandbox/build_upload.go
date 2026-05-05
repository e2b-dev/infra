package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Upload struct {
	buildID        uuid.UUID
	snap           *Snapshot
	paths          storage.Paths
	uploads        *Uploads
	store          storage.StorageProvider
	mem            storage.CompressConfig
	root           storage.CompressConfig
	objectMetadata storage.ObjectMetadata
	future         *utils.ErrorOnce
}

func NewUpload(
	ctx context.Context,
	uploads *Uploads,
	snap *Snapshot,
	store storage.StorageProvider,
	cfg storage.CompressConfig,
	ff *featureflags.Client,
	useCase string,
	objectMetadata storage.ObjectMetadata,
) (*Upload, error) {
	mem, err := resolveCompressConfig(ctx, cfg, ff, storage.MemfileName, snap.MemfileDiffHeader.Metadata.BlockSize, useCase)
	if err != nil {
		return nil, fmt.Errorf("resolve memfile compress config: %w", err)
	}
	root, err := resolveCompressConfig(ctx, cfg, ff, storage.RootfsName, snap.RootfsDiffHeader.Metadata.BlockSize, useCase)
	if err != nil {
		return nil, fmt.Errorf("resolve rootfs compress config: %w", err)
	}

	u := &Upload{
		buildID:        snap.BuildID,
		snap:           snap,
		paths:          storage.Paths{BuildID: snap.BuildID.String()},
		uploads:        uploads,
		store:          store,
		mem:            mem,
		root:           root,
		objectMetadata: objectMetadata,
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

func (u *Upload) Run(ctx context.Context) error {
	if !u.mem.IsCompressionEnabled() && !u.root.IsCompressionEnabled() {
		return u.runV3(ctx)
	}

	return u.runV4(ctx)
}

// Finish signals the upload's terminal outcome. Same-orch waiters wake on the
// future; cross-orch waiters wake on the Redis hint published here.
func (u *Upload) Finish(ctx context.Context, uploadErr error) {
	if u.future != nil {
		_ = u.future.SetError(uploadErr)
	}
	if u.uploads != nil {
		u.uploads.publishUploadDoneToRedis(ctx, u.buildID, uploadErr)
	}
}

// publish swaps a finalized header into the local cached device so peers and
// Wait()ers see the build as complete. ErrBuildNotInCache is the one acceptable
// failure mode: nothing was cached locally, nothing to swap.
func (u *Upload) publish(ctx context.Context, t build.DiffType, h *headers.Header) error {
	if u.uploads == nil {
		return nil
	}

	dev, err := u.uploads.find(ctx, u.buildID, t)
	if errors.Is(err, ErrBuildNotInCache) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load %s for swap: %w", t, err)
	}

	dev.SwapHeader(h)

	return nil
}

// resolveCompressConfig returns the effective compression config for a given
// file type and use case. Feature flags override the base config when active.
// Returns zero-value CompressConfig when compression is disabled.
//
// fileType and useCase are added to the LD evaluation context so that
// LaunchDarkly targeting rules can differentiate (e.g. compress memfile
// but not rootfs, or compress builds but not pauses). blockSize is the
// in-VM read granularity for this fileType (from the diff header) and
// constrains the legal frame sizes — see validateCompressConfig.
//
// The resolved config is validated; an invalid env or LD-derived config
// surfaces as an error so the upload fails fast rather than streaming with
// a misconfigured frame size.
func resolveCompressConfig(ctx context.Context, base storage.CompressConfig, ff *featureflags.Client, fileType string, blockSize uint64, useCase string) (storage.CompressConfig, error) {
	resolved := base

	if ff != nil {
		var extra []ldcontext.Context
		if fileType != "" {
			extra = append(extra, featureflags.CompressFileTypeContext(fileType))
		}
		if useCase != "" {
			extra = append(extra, featureflags.CompressUseCaseContext(useCase))
		}
		ctx = featureflags.AddToContext(ctx, extra...)

		v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()

		if v.Get("compressBuilds").BoolValue() {
			ct := v.Get("compressionType").StringValue()
			ldCfg := storage.CompressConfig{
				Enabled:            true,
				Type:               ct,
				Level:              v.Get("compressionLevel").IntValue(),
				FrameSizeKB:        v.Get("frameSizeKB").IntValue(),
				MinPartSizeMB:      v.Get("minPartSizeMB").IntValue(),
				FrameEncodeWorkers: v.Get("frameEncodeWorkers").IntValue(),
				EncoderConcurrency: v.Get("encoderConcurrency").IntValue(),
			}
			if ldCfg.CompressionType() != storage.CompressionNone {
				resolved = ldCfg
			}
		}
	}

	if !resolved.IsCompressionEnabled() {
		return storage.CompressConfig{}, nil
	}

	if err := validateCompressConfig(resolved, blockSize); err != nil {
		return storage.CompressConfig{}, err
	}

	return resolved, nil
}

// validateCompressConfig checks that the resolved config is internally
// consistent for the given block size. Frame size must be a positive multiple
// of blockSize so that every block-sized read served by the chunker lies
// inside one frame — otherwise Chunker.fetch fetches only the start frame and
// cache.sliceDirect returns uninitialized mmap bytes for the tail.
func validateCompressConfig(c storage.CompressConfig, blockSize uint64) error {
	fs := c.FrameSize()
	if fs <= 0 {
		return fmt.Errorf("frame size must be positive, got %d KB", c.FrameSizeKB)
	}
	if blockSize == 0 {
		return errors.New("block size must be positive")
	}
	if uint64(fs)%blockSize != 0 {
		return fmt.Errorf("frame size (%d) must be a multiple of block size (%d)", fs, blockSize)
	}

	return nil
}
