//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"

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
	useCase        string
	objectMetadata storage.ObjectMetadata
	future         *utils.ErrorOnce
	useV4          bool
	headerVersion  uint64
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
	// Filesystem-only snapshots have no memfile (NoDiff, block size 0), so
	// resolving its compress config would fail validation ("block size must be
	// positive"). The memfile body and header are never uploaded anyway.
	var mem storage.CompressConfig
	var memV4 bool
	var err error
	if !snap.FilesystemSnapshot {
		mem, memV4, err = resolveCompressConfig(ctx, cfg, ff, storage.MemfileName, snap.MemorySnapshot.BlockSize, useCase)
		if err != nil {
			return nil, fmt.Errorf("resolve memfile compress config: %w", err)
		}
	}
	root, rootV4, err := resolveCompressConfig(ctx, cfg, ff, storage.RootfsName, snap.RootfsBlockSize, useCase)
	if err != nil {
		return nil, fmt.Errorf("resolve rootfs compress config: %w", err)
	}

	if useCase != "" {
		ctx = featureflags.AddToContext(ctx, featureflags.CompressUseCaseContext(useCase))
	}
	headerVersion := uint64(headers.MetadataVersionV4)
	if ff != nil && ff.BoolFlag(ctx, featureflags.HeaderV5WriteFlag) {
		headerVersion = headers.MetadataVersionV5
	}

	u := &Upload{
		buildID:        snap.BuildID,
		snap:           snap,
		paths:          storage.Paths{BuildID: snap.BuildID.String()},
		uploads:        uploads,
		store:          store,
		mem:            mem,
		root:           root,
		useCase:        useCase,
		objectMetadata: objectMetadata,
		useV4:          memV4 || rootV4 || headerVersion == headers.MetadataVersionV5,
		headerVersion:  headerVersion,
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

// layerSizeMetadata adds the layer's logical, mapped, and diff sizes (all
// uncompressed, from the diff header) to the base object metadata. They live on
// the data object because the memfile values depend on the async dedup header.
func (u *Upload) layerSizeMetadata(h *headers.Header) storage.ObjectMetadata {
	md := maps.Clone(u.objectMetadata)
	if md == nil {
		md = make(storage.ObjectMetadata)
	}
	if h == nil || h.Metadata == nil {
		return md
	}

	bytesByBuild := h.Mapping.BytesByBuild()
	var mapped uint64
	for _, b := range bytesByBuild {
		mapped += b
	}
	md[storage.ObjectMetadataLogicalSize] = strconv.FormatUint(h.Metadata.Size, 10)
	md[storage.ObjectMetadataMappedSize] = strconv.FormatUint(mapped, 10)
	md[storage.ObjectMetadataDiffSize] = strconv.FormatUint(bytesByBuild[h.Metadata.BuildId], 10)

	return md
}

func (u *Upload) Run(ctx context.Context) error {
	// Attach the upload use case so flag reads can target it (e.g. write-through only for builds).
	ctx = featureflags.AddToContext(ctx, featureflags.CompressUseCaseContext(u.useCase))

	if !u.mem.IsCompressionEnabled() && !u.root.IsCompressionEnabled() && !u.useV4 {
		return u.runV3(ctx)
	}

	return u.runV4(ctx)
}

// Wait blocks until the upload has reached its terminal outcome (the future set
// by Finish) or ctx is done, returning the upload error. It lets a caller order
// work after the snapshot has durably landed — e.g. re-uploading the metadata
// object without racing the upload's own metadata write.
func (u *Upload) Wait(ctx context.Context) error {
	if u.future == nil {
		return nil
	}

	return u.future.WaitWithContext(ctx)
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
// file type and use case, plus whether the V4 header layout should be used for
// an uncompressed upload. Feature flags override the base config when active.
// Returns zero-value CompressConfig when compression is disabled. fileType,
// useCase are added to the LD evaluation context; blockSize constrains legal
// frame sizes — see validateCompressConfig.
func resolveCompressConfig(ctx context.Context, base storage.CompressConfig, ff *featureflags.Client, fileType string, blockSize uint64, useCase string) (storage.CompressConfig, bool, error) {
	resolved := base
	var useV4 bool

	if ff != nil {
		var extra []ldcontext.Context
		if fileType != "" {
			extra = append(extra, featureflags.CompressFileTypeContext(fileType))
		}
		if useCase != "" {
			extra = append(extra, featureflags.CompressUseCaseContext(useCase))
		}
		ctx = featureflags.AddToContext(ctx, extra...)

		useV4 = ff.BoolFlag(ctx, featureflags.V4HeaderForUncompressedFlag)

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
		return storage.CompressConfig{}, useV4, nil
	}

	if err := validateCompressConfig(resolved, blockSize); err != nil {
		return storage.CompressConfig{}, false, err
	}

	return resolved, useV4, nil
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
