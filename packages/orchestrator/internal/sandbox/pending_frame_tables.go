package sandbox

import (
	"fmt"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// PendingFrameTables collects FrameTables from compressed data uploads across
// all layers. After all data files are uploaded, the collected tables are applied
// to headers before the compressed headers are serialized and uploaded.
type PendingFrameTables struct {
	tables sync.Map // key: "buildId/fileType", value: *storage.FrameTable
}

func pendingFrameTableKey(buildID, fileType string) string {
	return buildID + "/" + fileType
}

func (p *PendingFrameTables) add(key string, ft *storage.FrameTable) {
	if ft == nil {
		return
	}

	p.tables.Store(key, ft)
}

func (p *PendingFrameTables) get(key string) *storage.FrameTable {
	v, ok := p.tables.Load(key)
	if !ok {
		return nil
	}

	return v.(*storage.FrameTable)
}

func (p *PendingFrameTables) applyToHeader(h *header.Header, fileType string) error {
	if h == nil {
		return nil
	}

	for _, mapping := range h.Mapping {
		key := pendingFrameTableKey(mapping.BuildId.String(), fileType)
		ft := p.get(key)

		if ft == nil {
			continue
		}

		if err := mapping.AddFrames(ft); err != nil {
			return fmt.Errorf("apply frames to mapping at offset %#x for build %s: %w",
				mapping.Offset, mapping.BuildId.String(), err)
		}
	}

	return nil
}
