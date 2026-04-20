package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// OnRemoveFn is invoked after a sandbox's network slot is released.
type OnRemoveFn func(ctx context.Context, sbx *Sandbox)

// NetworkMap provides O(1) lookups from host IP to sandbox.
// It is managed independently of the live sandbox map — Insert is called
// when a network slot is assigned, Remove when the slot is released.
type NetworkMap struct {
	m        *smap.Map[*Sandbox]
	onRemove OnRemoveFn
}

func NewNetworkMap(onRemove OnRemoveFn) *NetworkMap {
	return &NetworkMap{m: smap.New[*Sandbox](), onRemove: onRemove}
}

// AssignNetwork registers a sandbox's IP so it is findable by GetByHostPort.
func (nm *NetworkMap) AssignNetwork(ctx context.Context, sbx *Sandbox) {
	ip := sbx.Slot.HostIPString()
	nm.m.Insert(ip, sbx)

	logger.L().Info(ctx, "sandbox network map entry added",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(ip),
	)
}

// NetworkReleased unregisters a sandbox's IP, guarding against the slot
// having been reused by a new sandbox, and invokes the onRemove callback
// after a successful removal.
func (nm *NetworkMap) NetworkReleased(ctx context.Context, sbx *Sandbox) {
	ip := sbx.Slot.HostIPString()

	removed := nm.m.RemoveCb(ip, func(_ string, v *Sandbox, exists bool) bool {
		return exists && v == sbx
	})

	if !removed {
		return
	}

	logger.L().Info(ctx, "sandbox network map entry removed",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(ip),
	)

	if nm.onRemove != nil {
		nm.onRemove(ctx, sbx)
	}
}

// GetByHostPort looks up a sandbox by its host IP address parsed from hostPort.
func (nm *NetworkMap) GetByHostPort(hostPort string) (*Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", hostPort, err)
	}

	sbx, ok := nm.m.Get(reqIP)
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
	OnRemove(ctx context.Context, sbx *Sandbox)
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

// MarkRunning makes the sandbox visible to Get/Items/Count and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
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

func NewSandboxesMap() *Map {
	m := &Map{
		live: smap.New[*Sandbox](),
	}
	m.NetworkMap = NewNetworkMap(func(ctx context.Context, sbx *Sandbox) {
		go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
			s.OnRemove(ctx, sbx)
		})
	})

	return m
}
