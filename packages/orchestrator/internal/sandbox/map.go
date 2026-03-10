package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type MapSubscriber interface {
	OnInsert(sandbox *Sandbox)
	OnRemove(sandboxID string)
}

type Map struct {
	sandboxes *smap.Map[*Sandbox]

	subs     []MapSubscriber
	subsLock sync.RWMutex
}

func (m *Map) Subscribe(subscriber MapSubscriber) {
	m.subsLock.Lock()
	defer m.subsLock.Unlock()

	m.subs = append(m.subs, subscriber)
}

func (m *Map) trigger(fn func(MapSubscriber)) {
	m.subsLock.RLock()
	defer m.subsLock.RUnlock()

	for _, subscriber := range m.subs {
		fn(subscriber)
	}
}

func (m *Map) Items() map[string]*Sandbox {
	all := m.sandboxes.Items()
	result := make(map[string]*Sandbox, len(all))
	for k, v := range all {
		if v.IsRunning() {
			result[k] = v
		}
	}

	return result
}

func (m *Map) Count() int {
	count := 0
	for _, v := range m.sandboxes.Items() {
		if v.IsRunning() {
			count++
		}
	}

	return count
}

func (m *Map) Get(sandboxID string) (*Sandbox, bool) {
	sbx, ok := m.sandboxes.Get(sandboxID)
	if !ok || !sbx.IsRunning() {
		return nil, false
	}

	return sbx, true
}

// GetByHostPort looks up a sandbox by its host IP address parsed from hostPort.
// It matches any sandbox in the map (starting or running).
func (m *Map) GetByHostPort(hostPort string) (*Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", hostPort, err)
	}

	for _, sbx := range m.sandboxes.Items() {
		if sbx.Slot.HostIPString() == reqIP {
			return sbx, nil
		}
	}

	return nil, fmt.Errorf("sandbox with address %s not found", hostPort)
}

func (m *Map) Insert(ctx context.Context, sbx *Sandbox) {
	logger.L().Info(ctx, "adding sandbox to map",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithTemplateID(sbx.Runtime.TemplateID),
		logger.WithBuildID(sbx.Runtime.BuildID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)

	m.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
}

// MarkRunning transitions a sandbox from starting to running and notifies
// OnInsert subscribers.
func (m *Map) MarkRunning(sbx *Sandbox) {
	sbx.started.Store(true)

	go m.trigger(func(s MapSubscriber) {
		s.OnInsert(sbx)
	})
}

func (m *Map) Remove(ctx context.Context, sandboxID string) {
	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, _ *Sandbox, exists bool) bool {
		return exists
	})

	if removed {
		logger.L().Info(ctx, "removing sandbox from map", logger.WithSandboxID(sandboxID))

		go m.trigger(func(s MapSubscriber) {
			s.OnRemove(sandboxID)
		})
	}
}

func (m *Map) RemoveByLifecycleID(ctx context.Context, sandboxID, lifecycleID string) {
	logger.L().Info(ctx, "removing sandbox from map by lifecycle ID",
		logger.WithSandboxID(sandboxID),
	)

	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if v == nil {
			return false
		}

		return v.LifecycleID == lifecycleID
	})

	if removed {
		go m.trigger(func(s MapSubscriber) {
			s.OnRemove(sandboxID)
		})
	}
}

func NewSandboxesMap() *Map {
	return &Map{sandboxes: smap.New[*Sandbox]()}
}
