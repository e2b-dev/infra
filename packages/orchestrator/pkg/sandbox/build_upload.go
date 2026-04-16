package sandbox

import (
	"bytes"
	"context"
	"slices"
	"sync"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// BuildUploader uploads a paused snapshot's files to storage.
type BuildUploader interface {
	// UploadData uploads data files, snapfile, and metadata.
	UploadData(ctx context.Context) error
	// FinalizeHeaders uploads final headers after all upstream layers are done.
	// Returns serialized V4 header bytes for peer transition (nil for uncompressed).
	FinalizeHeaders(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error)
}

// NewBuildUploader creates a BuildUploader for the given snapshot.
//
// Compression config is resolved per file (memfile, rootfs) using the base
// config, feature flags, and use case. If neither file has compression enabled,
// returns a V3 (uncompressed) uploader; otherwise a V4 (compressed) uploader
// with the two resolved configs.
//
// pending is shared across layers for multi-layer builds; nil is fine for
// single-layer.
func NewBuildUploader(ctx context.Context, snapshot *Snapshot, persistence storage.StorageProvider, paths storage.Paths, cfg storage.CompressConfig, ff *featureflags.Client, useCase string, pending *PendingBuildInfo) BuildUploader {
	base := buildUploader{
		paths:       paths,
		persistence: persistence,
		snapshot:    snapshot,
	}

	memCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.MemfileName, useCase)
	rootfsCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.RootfsName, useCase)

	if !memCfg.IsCompressionEnabled() && !rootfsCfg.IsCompressionEnabled() {
		return &uncompressedUploader{buildUploader: base}
	}

	if pending == nil {
		pending = &PendingBuildInfo{}
	}

	return &compressedUploader{
		buildUploader: base,
		pending:       pending,
		memCfg:        memCfg,
		rootfsCfg:     rootfsCfg,
	}
}

// buildUploader contains fields and helpers shared by both implementations.
type buildUploader struct {
	paths       storage.Paths
	persistence storage.StorageProvider
	snapshot    *Snapshot
}

type pendingBuildInfo struct {
	ft       *storage.FrameTable
	fileSize int64
	checksum [32]byte
}

// PendingBuildInfo collects FrameTables and file sizes from compressed data
// uploads across all layers. After all data files are uploaded, the collected
// tables are applied to headers before the compressed headers are serialized
// and uploaded. Safe for concurrent use from errgroup goroutines.
type PendingBuildInfo struct {
	mu sync.Mutex
	m  map[string]pendingBuildInfo
}

func pendingBuildInfoKey(buildID, fileType string) string {
	return buildID + "/" + fileType
}

func (p *PendingBuildInfo) add(key string, ft *storage.FrameTable, fileSize int64, checksum [32]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.m == nil {
		p.m = make(map[string]pendingBuildInfo)
	}

	p.m[key] = pendingBuildInfo{ft: ft, fileSize: fileSize, checksum: checksum}
}

func (p *PendingBuildInfo) get(key string) *pendingBuildInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, ok := p.m[key]
	if !ok {
		return nil
	}

	return &info
}

func (p *PendingBuildInfo) applyToHeader(h *headers.Header, fileType string) {
	if h == nil {
		return
	}

	buildIDs := make([]uuid.UUID, len(h.Mapping))
	for i, m := range h.Mapping {
		buildIDs[i] = m.BuildId
	}
	slices.SortFunc(buildIDs, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	buildIDs = slices.CompactFunc(buildIDs, func(a, b uuid.UUID) bool { return a == b })

	for _, buildID := range buildIDs {
		key := pendingBuildInfoKey(buildID.String(), fileType)
		info := p.get(key)

		if info == nil {
			// Parent builds and uuid.Nil (empty blocks) have no pending entry.
			// Parent builds are either already in h.Builds (copied by ToDiffHeader),
			// or h.Builds is nil (V3 base with no Builds map at all).
			continue
		}

		if h.Builds == nil {
			h.Builds = make(map[uuid.UUID]headers.BuildData)
		}

		bd := headers.BuildData{
			Size:     info.fileSize,
			Checksum: info.checksum,
		}
		if info.ft != nil && info.ft.IsCompressed() {
			bd.FrameData = info.ft
		}
		h.Builds[buildID] = bd
	}
}
