package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// NetworkMap provides O(1) lookups from host IP to sandbox.
// It is managed independently of the live sandbox map — Insert is called
// when a network slot is assigned, Remove when the slot is released.
type NetworkMap struct {
	m *smap.Map[*Sandbox]
}

func NewNetworkMap() *NetworkMap {
	return &NetworkMap{m: smap.New[*Sandbox]()}
}

// Insert registers a sandbox's IP so it is findable by GetByHostPort.
func (idx *NetworkMap) Insert(sbx *Sandbox) {
	idx.m.Insert(sbx.Slot.HostIPString(), sbx)
}

// Remove unregisters a sandbox's IP, guarding against the slot having
// been reused by a new sandbox.
func (idx *NetworkMap) Remove(sbx *Sandbox) {
	idx.m.RemoveCb(sbx.Slot.HostIPString(), func(_ string, v *Sandbox, exists bool) bool {
		return exists && v == sbx
	})
}

// GetByHostPort looks up a sandbox by its host IP address parsed from hostPort.
func (idx *NetworkMap) GetByHostPort(hostPort string) (*Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", hostPort, err)
	}

	sbx, ok := idx.m.Get(reqIP)
	if !ok {
		return nil, fmt.Errorf("sandbox not found")
	}

	return sbx, nil
}

// MapSubscriber receives lifecycle notifications from the sandbox Map.
type MapSubscriber interface {
	// OnInsert is triggered when a sandbox transitions to the running state.
	OnInsert(ctx context.Context, sandbox *Sandbox)
	// OnRemove is triggered when a sandbox's network slot is released.
	OnRemove(ctx context.Context, sandbox *Sandbox)
}

// Map holds sandboxes that are live (running). It is independent of the
// NetworkMap — the two are managed through separate Insert/Remove calls.
type Map struct {
	live *smap.Map[*Sandbox]
	// NetworkMap maps host IP string to sandbox for O(1) reverse lookups.
	// Managed independently: Insert when slot is assigned, Remove when released.
	NetworkMap *NetworkMap

	subs     []MapSubscriber
	subsLock sync.RWMutex
}

func (m *Map) Subscribe(subscriber MapSubscriber) {
	m.subsLock.Lock()
	defer m.subsLock.Unlock()

	m.subs = append(m.subs, subscriber)
}

func (m *Map) trigger(ctx context.Context, fn func(context.Context, MapSubscriber)) {
	m.subsLock.RLock()
	defer m.subsLock.RUnlock()

	for _, subscriber := range m.subs {
		fn(ctx, subscriber)
	}
}

func (m *Map) Items() map[string]*Sandbox {
	return m.live.Items()
}

func (m *Map) Count() int {
	return m.live.Count()
}

func (m *Map) Get(sandboxID string) (*Sandbox, bool) {
	return m.live.Get(sandboxID)
}

// SandboxStarted makes the sandbox visible to Get/Items/Count and notifies OnInsert subscribers.
func (m *Map) SandboxStarted(ctx context.Context, sbx *Sandbox) {
	if !m.live.InsertIfAbsent(sbx.Runtime.SandboxID, sbx) {
		return
	}

	go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnInsert(ctx, sbx)
	})
}

// MarkStopped removes the sandbox from live queries (Get, Items, Count).
// Returns true if the sandbox was successfully removed.
func (m *Map) MarkStopped(ctx context.Context, sandboxID, lifecycleID string) bool {
	stopped := false

	m.live.RemoveCb(sandboxID, func(_ string, sbx *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if sbx.LifecycleID != lifecycleID {
			return false
		}

		logger.L().Info(ctx, "sandbox stopped",
			logger.WithSandboxID(sandboxID),
			logger.WithLifecycleID(lifecycleID),
			logger.WithSandboxIP(sbx.Slot.HostIPString()),
		)

		stopped = true

		return true
	})

	return stopped
}

// SandboxRemoved notifies OnRemove subscribers. Called after all sandbox
// resources (network slot, files, etc.) have been released.
func (m *Map) SandboxRemoved(ctx context.Context, sbx *Sandbox) {
	logger.L().Info(ctx, "sandbox removed",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)

	go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnRemove(ctx, sbx)
	})
}

func NewSandboxesMap() *Map {
	return &Map{
		live:       smap.New[*Sandbox](),
		NetworkMap: NewNetworkMap(),
	}
}
