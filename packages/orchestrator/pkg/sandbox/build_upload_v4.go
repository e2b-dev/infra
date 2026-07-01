//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func (u *Upload) runV4(ctx context.Context) error {
	memSrc, err := u.snap.MemorySnapshot.Diff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("memfile diff path: %w", err)
	}

	rootfsSrc, err := u.snap.RootfsDiff.CachePath(ctx)
	if err != nil {
		return fmt.Errorf("rootfs diff path: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		h, err := u.snap.MemorySnapshot.DiffHeader.WaitWithContext(ctx)
		if err != nil {
			return fmt.Errorf("wait memfile diff header: %w", err)
		}
		if h == nil {
			return nil
		}

		return u.uploadFramed(ctx, build.Memfile, memSrc, h, u.mem)
	})

	eg.Go(func() error {
		h, err := u.snap.RootfsDiffHeader.WaitWithContext(ctx)
		if err != nil {
			return fmt.Errorf("wait rootfs diff header: %w", err)
		}
		if h == nil {
			return nil
		}

		return u.uploadFramed(ctx, build.Rootfs, rootfsSrc, h, u.root)
	})

	meta := storage.WithMetadata(u.objectMetadata)

	eg.Go(func() error {
		// Filesystem-only snapshots resume by reboot, not snapfile restore, so
		// the snapfile (created only for its disk-flush side effect) is not uploaded.
		if u.snap.FilesystemSnapshot {
			return nil
		}

		return uploadBlobWithMetrics(ctx, u.store, u.paths.Snapfile(), u.snap.Snapfile.Path(), uploadFileSnap, meta)
	})

	eg.Go(func() error {
		return uploadBlobWithMetrics(ctx, u.store, u.paths.Metadata(), u.snap.Metafile.Path(), uploadFileMeta, meta)
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
		fullFT, checksum, err := storage.UploadFramed(ctx, u.store, u.paths.DataFile(string(fileType), cfg.CompressionType()), srcPath, storage.WithCompressConfig(cfg), storage.WithMetadata(u.layerSizeMetadata(srcHeader)), storage.WithChecksumSHA256())
		if err != nil {
			return fmt.Errorf("%s upload: %w", fileType, err)
		}

		// Compressed: frame-table byte count, since sparse memfile diffs stream
		// fewer bytes than they occupy on disk. Uncompressed has no table.
		ft := fullFT.Table()
		size := ft.UncompressedSize()
		compressedSize := ft.CompressedSize()
		if !ft.IsCompressed() {
			info, statErr := os.Stat(srcPath)
			if statErr != nil {
				return fmt.Errorf("%s stat: %w", fileType, statErr)
			}
			size = info.Size()
			compressedSize = size
		}

		dataFileType := uploadFileMemfile
		if fileType == build.Rootfs {
			dataFileType = uploadFileRootfs
		}
		recordUploadCompression(ctx, dataFileType, cfg, size, compressedSize)
		selfBuild = headers.BuildData{Size: size, Checksum: checksum, FrameData: ft}
	}

	h := srcHeader.CloneForUpload(u.headerVersion)
	h.IncompletePendingUpload = false
	if h.Builds == nil {
		h.Builds = make(map[uuid.UUID]headers.BuildData)
	}

	if err := u.appendAncestorBuilds(ctx, h.Builds, srcHeader.Mapping, fileType); err != nil {
		return err
	}
	h.Builds[u.buildID] = selfBuild

	headerFileType := uploadFileMemfileHeader
	if fileType == build.Rootfs {
		headerFileType = uploadFileRootfsHeader
	}
	if err := storeHeaderWithMetrics(ctx, u.store, u.paths.HeaderFile(string(fileType)), headerFileType, h, storage.WithMetadata(u.objectMetadata)); err != nil {
		return fmt.Errorf("store %s header: %w", fileType, err)
	}

	return u.publish(ctx, fileType, h)
}

// appendAncestorBuilds waits on every unique buildID referenced by mappings
// (excluding self) — gating publish on parents' header finalization — and,
// when dst is non-nil, writes the freshest BuildData into it. Existing dst
// entries are overwritten (Wait is more authoritative than CloneForUpload).
// Skips silently when Wait returns nil.
//
// V3 ancestors carry no Builds map, so a sentinel empty BuildData{} is
// written — the entry's presence alone is what matters: GetBuildFrameData
// returns UncompressedFrameTable (nil FrameData → sentinel), and createDiff's
// hasEntry branch handles size=0 by falling back to upstream.Size. We avoid
// computing the diff size here on purpose: it's not in Metadata.Size (that's
// the virtual size), and asking storage at upload time across a long
// ancestor chain would multiply roundtrips. The fallback amortizes into the
// read that's about to happen anyway.
//
// V3 callers pass dst=nil — they need the barrier but have no Builds map.
//
// Local ancestors resolve from the in-memory futures map without I/O;
// cross-orch ancestors take a single remote storage round-trip. Sequential —
// the critical path is the slowest pending Wait either way.
func (u *Upload) appendAncestorBuilds(
	ctx context.Context,
	dst map[uuid.UUID]headers.BuildData,
	mappings headers.Mapping,
	fileType build.DiffType,
) error {
	if u.uploads == nil {
		return nil
	}

	// Mapping.Builds() is already deduplicated, so no local seen-set is needed.
	for _, buildID := range mappings.Builds() {
		if buildID == u.buildID || buildID == uuid.Nil {
			continue
		}

		h, err := u.uploads.Wait(ctx, buildID, fileType)
		if err != nil {
			return fmt.Errorf("wait for ancestor %s/%s: %w", buildID, fileType, err)
		}
		if h == nil || dst == nil {
			continue
		}

		if bd, ok := h.Builds[buildID]; ok {
			dst[buildID] = bd

			continue
		}

		if h.Metadata.Version < headers.MetadataVersionV4 {
			dst[buildID] = headers.BuildData{}
		}
	}

	return nil
}
