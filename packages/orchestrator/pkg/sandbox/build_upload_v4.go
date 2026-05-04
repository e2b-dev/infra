package sandbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Upload) runV4(ctx context.Context) error {
	memSrc, err := u.snap.MemfileDiff.CachePath()
	if err != nil {
		return fmt.Errorf("memfile diff path: %w", err)
	}

	rootfsSrc, err := u.snap.RootfsDiff.CachePath()
	if err != nil {
		return fmt.Errorf("rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	if u.snap.MemfileDiffHeader != nil {
		eg.Go(func() error {
			return u.uploadFramed(ctx, build.Memfile, memSrc, u.snap.MemfileDiffHeader, u.mem)
		})
	}

	if u.snap.RootfsDiffHeader != nil {
		eg.Go(func() error {
			return u.uploadFramed(ctx, build.Rootfs, rootfsSrc, u.snap.RootfsDiffHeader, u.root)
		})
	}

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Snapfile(), storage.SnapfileObjectType, u.snap.Snapfile.Path())
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Metadata(), storage.MetadataObjectType, u.snap.Metafile.Path())
	})

	return eg.Wait()
}

func (u *Upload) uploadFramed(
	ctx context.Context,
	fileType build.DiffType,
	srcPath string,
	srcHeader *headers.Header,
	cfg storage.CompressConfig,
) error {
	var selfBuild headers.BuildData

	if srcPath != "" {
		ft, checksum, err := storage.UploadFramed(ctx, u.store, u.paths.DataFile(string(fileType), cfg.CompressionType()), seekableTypeFor(fileType), srcPath, cfg)
		if err != nil {
			return fmt.Errorf("%s upload: %w", fileType, err)
		}

		// FrameTable count, not os.Stat: sparse memfile diffs stream less than
		// they appear on disk.
		selfBuild = headers.BuildData{Size: ft.UncompressedSize(), Checksum: checksum}
		if ft.IsCompressed() {
			selfBuild.FrameData = ft
		}
	}

	h := srcHeader.CloneForUpload(headers.MetadataVersionV4)
	h.IncompletePendingUpload = false

	// Dependency closure is the set of buildIDs referenced by mappings, minus
	// self. Each ancestor's BuildData lives in its own finalized header's
	// self-entry; Wait routes to local future, peer, or remote storage as
	// appropriate. Already-final ancestors resolve immediately (remote storage
	// round-trip beats blocking on whatever the immediate parent's upload is
	// doing).
	ancestors, err := u.collectAncestorBuilds(ctx, srcHeader.Mapping, fileType)
	if err != nil {
		return err
	}

	// Empty diffs still represent a layer descendants must record as an ancestor.
	h.Builds = ancestors
	h.Builds[u.buildID] = selfBuild

	if err := headers.StoreHeader(ctx, u.store, u.paths.HeaderFile(string(fileType)), h); err != nil {
		return fmt.Errorf("store %s header: %w", fileType, err)
	}

	return u.publish(ctx, fileType, h)
}

// collectAncestorBuilds resolves every unique buildID referenced by mappings
// (excluding self) to its finalized BuildData. Local ancestors resolve from the
// in-memory futures map without any I/O; cross-orch ancestors take a single
// remote storage round-trip each. Sequential — the critical path is the slowest
// pending Wait either way, and serial keeps the code simple.
func (u *Upload) collectAncestorBuilds(
	ctx context.Context,
	mappings []headers.BuildMap,
	fileType build.DiffType,
) (map[uuid.UUID]headers.BuildData, error) {
	out := make(map[uuid.UUID]headers.BuildData)
	if u.uploads == nil {
		return out, nil
	}

	for _, m := range mappings {
		if m.BuildId == u.buildID || m.BuildId == uuid.Nil {
			continue
		}
		if _, dup := out[m.BuildId]; dup {
			continue
		}

		h, err := u.uploads.Wait(ctx, m.BuildId, fileType)
		if err != nil {
			return nil, fmt.Errorf("wait for ancestor %s/%s: %w", m.BuildId, fileType, err)
		}
		// V3 ancestors have Builds=nil (FrameTable is V4-only); their data is
		// raw bytes and the read path doesn't consult Builds for them. Skip
		// silently so V4 descendants of V3 ancestors still upload.
		bd, ok := h.Builds[m.BuildId]
		if !ok {
			continue
		}

		out[m.BuildId] = bd
	}

	return out, nil
}

func seekableTypeFor(fileType build.DiffType) storage.SeekableObjectType {
	switch fileType {
	case build.Memfile:
		return storage.MemfileObjectType
	case build.Rootfs:
		return storage.RootFSObjectType
	}

	return storage.UnknownSeekableObjectType
}
