package header

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Dependency struct {
	Size       int64
	Checksum   [32]byte
	FrameTable *storage.FrameTable
}

func NewHeaderWithResolvedDependencies(metadata *Metadata, mapping []BuildMap, selfDep Dependency, parent map[uuid.UUID]Dependency) (*Header, error) {
	h, err := NewHeader(metadata, mapping)
	if err != nil {
		return nil, err
	}
	h.self = utils.NewSetOnce[Dependency]()
	if err := h.self.SetValue(selfDep); err != nil {
		return nil, fmt.Errorf("set self dep: %w", err)
	}
	if len(parent) > 0 {
		h.parent.Store(&parent)
	}

	return h, nil
}

func newHeaderWithPendingDependencies(metadata *Metadata, mapping []BuildMap, parent *Header) (*Header, error) {
	h, err := NewHeader(metadata, mapping)
	if err != nil {
		return nil, err
	}
	h.self = utils.NewSetOnce[Dependency]()

	if parent != nil {
		src := parent.parentDependencies()
		merged := make(map[uuid.UUID]Dependency, len(src)+1)
		maps.Copy(merged, src)
		if dep, err := parent.selfDependency(); err == nil {
			merged[parent.Metadata.BuildId] = dep
		}
		h.parent.Store(&merged)
	}

	return h, nil
}

func (t *Header) LookupDependency(buildID uuid.UUID) Dependency {
	if buildID == t.Metadata.BuildId {
		dep, _ := t.selfDependency()

		return dep
	}

	return t.parentDependencies()[buildID]
}

// WaitUntilFinal — Finalize stores the refreshed parent map before publishing
// self, so a successful self.Result implies the parent snapshot is also final.
func (t *Header) WaitUntilFinal(ctx context.Context) error {
	if t.self == nil {
		return nil
	}
	_, err := t.self.WaitWithContext(ctx)

	return err
}

// SetParent must be called before Finalize so a single WaitUntilFinal
// observes both parent and self resolved.
func (t *Header) SetParent(finalParent *Header) {
	src := finalParent.parentDependencies()
	merged := make(map[uuid.UUID]Dependency, len(src)+1)
	maps.Copy(merged, src)
	if dep, err := finalParent.selfDependency(); err == nil {
		merged[finalParent.Metadata.BuildId] = dep
	}
	t.parent.Store(&merged)
}

func (t *Header) Finalize(dep Dependency) error {
	if t.self == nil {
		return errors.New("Finalize on born-final header")
	}

	return t.self.SetValue(dep)
}

// Swap adopts another instance's final state in place — used when a peer
// hands us our own finalized header. Parent-before-self ordering matches
// Finalize so WaitUntilFinal waiters see a consistent view. SetValue errors
// are dropped: a concurrent local Finalize and peer transition converge on
// the same value. Returns t for chaining.
func (t *Header) Swap(finalHeader *Header) *Header {
	if p := finalHeader.parent.Load(); p != nil {
		t.parent.Store(p)
	}
	if t.self == nil || finalHeader.self == nil {
		return t
	}
	if selfDep, err := finalHeader.self.Result(); err == nil {
		_ = t.self.SetValue(selfDep)
	}

	return t
}

// Cancel — err == nil guard lets callers use the deferred-Cancel-on-err
// pattern without branching on success.
func (t *Header) Cancel(err error) {
	if err == nil || t.self == nil {
		return
	}
	_ = t.self.SetError(err)
}

func (t *Header) parentDependencies() map[uuid.UUID]Dependency {
	if p := t.parent.Load(); p != nil {
		return *p
	}

	return nil
}

func (t *Header) selfDependency() (Dependency, error) {
	if t.self == nil {
		return Dependency{}, nil
	}

	return t.self.Result()
}
