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

	meta := storage.WithMetadata(u.objectMetadata)

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Snapfile(), storage.SnapfileObjectType, u.snap.Snapfile.Path(), meta)
	})

	eg.Go(func() error {
		return storage.UploadBlob(ctx, u.store, u.paths.Metadata(), storage.MetadataObjectType, u.snap.Metafile.Path(), meta)
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
		ft, checksum, err := storage.UploadFramed(ctx, u.store, u.paths.DataFile(string(fileType), cfg.CompressionType()), seekableTypeFor(fileType), srcPath, storage.WithCompressConfig(cfg), storage.WithMetadata(u.objectMetadata))
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
	if h.Builds == nil {
		h.Builds = make(map[uuid.UUID]headers.BuildData)
	}

	if err := u.appendAncestorBuilds(ctx, h.Builds, srcHeader.Mapping, fileType); err != nil {
		return err
	}
	h.Builds[u.buildID] = selfBuild

	if err := headers.StoreHeader(ctx, u.store, u.paths.HeaderFile(string(fileType)), h); err != nil {
		return fmt.Errorf("store %s header: %w", fileType, err)
	}

	return u.publish(ctx, fileType, h)
}

// appendAncestorBuilds waits on every unique buildID referenced by mappings
// (excluding self) — gating publish on parents' header finalization — and,
// when dst is non-nil, writes the freshest BuildData into it. Existing dst
// entries are overwritten (Wait is more authoritative than CloneForUpload).
// Skips silently when Wait returns nil or the ancestor carries no Builds
// entry (V3 ancestor); pre-existing dst entries are preserved.
//
// V3 callers pass dst=nil — they need the barrier but have no Builds map.
//
// Local ancestors resolve from the in-memory futures map without I/O;
// cross-orch ancestors take a single remote storage round-trip. Sequential —
// the critical path is the slowest pending Wait either way.
func (u *Upload) appendAncestorBuilds(
	ctx context.Context,
	dst map[uuid.UUID]headers.BuildData,
	mappings []headers.BuildMap,
	fileType build.DiffType,
) error {
	if u.uploads == nil {
		return nil
	}

	seen := make(map[uuid.UUID]struct{}, len(mappings))
	for _, m := range mappings {
		if m.BuildId == u.buildID || m.BuildId == uuid.Nil {
			continue
		}
		if _, dup := seen[m.BuildId]; dup {
			continue
		}
		seen[m.BuildId] = struct{}{}

		h, err := u.uploads.Wait(ctx, m.BuildId, fileType)
		if err != nil {
			return fmt.Errorf("wait for ancestor %s/%s: %w", m.BuildId, fileType, err)
		}
		if h == nil || dst == nil {
			continue
		}

		if bd, ok := h.Builds[m.BuildId]; ok {
			dst[m.BuildId] = bd
		}
	}

	return nil
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
