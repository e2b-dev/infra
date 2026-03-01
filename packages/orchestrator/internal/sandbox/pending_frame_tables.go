package sandbox

import (
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// pendingBuildInfo pairs a FrameTable with the uncompressed file size and
// compressed-data checksum so all can be stored in the header after uploads complete.
type pendingBuildInfo struct {
	ft       *storage.FrameTable
	fileSize int64
	checksum [32]byte
}

// PendingBuildInfo collects FrameTables and file sizes from compressed data
// uploads across all layers. After all data files are uploaded, the collected
// tables are applied to headers before the compressed headers are serialized
// and uploaded.
type PendingBuildInfo sync.Map

func pendingBuildInfoKey(buildID, fileType string) string {
	return buildID + "/" + fileType
}

func (p *PendingBuildInfo) add(key string, ft *storage.FrameTable, fileSize int64, checksum [32]byte) {
	if ft == nil {
		return
	}

	(*sync.Map)(p).Store(key, pendingBuildInfo{ft: ft, fileSize: fileSize, checksum: checksum})
}

func (p *PendingBuildInfo) get(key string) *pendingBuildInfo {
	v, ok := (*sync.Map)(p).Load(key)
	if !ok {
		return nil
	}

	info := v.(pendingBuildInfo)

	return &info
}

func (p *PendingBuildInfo) applyToHeader(h *header.Header, fileType string) error {
	if h == nil {
		return nil
	}

	for _, mapping := range h.Mapping {
		key := pendingBuildInfoKey(mapping.BuildId.String(), fileType)
		info := p.get(key)

		if info == nil {
			continue
		}

		if err := mapping.AddFrames(info.ft); err != nil {
			return fmt.Errorf("apply frames to mapping at offset %#x for build %s: %w",
				mapping.Offset, mapping.BuildId.String(), err)
		}
	}

	// Populate BuildFiles with sizes and checksums for this fileType's builds.
	for _, mapping := range h.Mapping {
		key := pendingBuildInfoKey(mapping.BuildId.String(), fileType)
		info := p.get(key)
		if info == nil {
			continue
		}

		if h.BuildFiles == nil {
			h.BuildFiles = make(map[uuid.UUID]header.BuildFileInfo)
		}
		h.BuildFiles[mapping.BuildId] = header.BuildFileInfo{
			Size:     info.fileSize,
			Checksum: info.checksum,
		}
	}

	return nil
}
