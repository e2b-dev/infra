//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// MapSubscriber receives lifecycle notifications from the sandbox Map.
//
// Callbacks are invoked synchronously from the goroutine that performed the
// state change. Implementations must be non-blocking; if async work is needed,
// the subscriber is responsible for dispatching it.
type MapSubscriber interface {
	// OnInsert is triggered when a sandbox transitions to the running state.
	OnInsert(ctx context.Context, sandbox *Sandbox)
	// OnNetworkRelease is triggered when a sandbox's network slot is released.
	OnNetworkRelease(ctx context.Context, sbx *Sandbox)
}

type SandboxState string

const (
	SandboxStateRunning  SandboxState = "running"
	SandboxStateStopping SandboxState = "stopping"
)

type lifecycleEntry struct {
	sandbox   *Sandbox
	stateLock sync.RWMutex
	state     SandboxState
}

func newLifecycleEntry(sbx *Sandbox, state SandboxState) *lifecycleEntry {
	return &lifecycleEntry{
		sandbox: sbx,
		state:   state,
	}
}

func (e *lifecycleEntry) setState(state SandboxState) {
	e.stateLock.Lock()
	defer e.stateLock.Unlock()

	e.state = state
}

func (e *lifecycleEntry) getState() SandboxState {
	e.stateLock.RLock()
	defer e.stateLock.RUnlock()

	return e.state
}

// Map holds sandboxes that are live (running), known active lifecycles,
// together with a IP-to-sandbox index. The indexes are managed independently.
//
// AssignNetwork/NetworkReleased manage the IP map,
// MarkRunning/MarkStopping manage the live set.
type Map struct {
	live       *smap.Map[*Sandbox]
	lifecycles *smap.Map[*lifecycleEntry]
	network    *smap.Map[*Sandbox]

	subs     []MapSubscriber
	subsLock sync.RWMutex
}

func NewSandboxesMap() *Map {
	return &Map{
		live:       smap.New[*Sandbox](),
		lifecycles: smap.New[*lifecycleEntry](),
		network:    smap.New[*Sandbox](),
	}
}

func sandboxLifecycleKey(sandboxID, lifecycleID string) string {
	return fmt.Sprintf("%s/%s", sandboxID, lifecycleID)
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

func (m *Map) LifecycleItems() []*Sandbox {
	entries := m.lifecycles.Items()
	sandboxes := make([]*Sandbox, 0, len(entries))
	for _, entry := range entries {
		sandboxes = append(sandboxes, entry.sandbox)
	}

	return sandboxes
}

func (m *Map) LifecycleItemsByState(states ...SandboxState) []*Sandbox {
	stateSet := make(map[SandboxState]struct{}, len(states))
	for _, state := range states {
		stateSet[state] = struct{}{}
	}

	entries := m.lifecycles.Items()
	sandboxes := make([]*Sandbox, 0, len(entries))
	for _, entry := range entries {
		if _, ok := stateSet[entry.getState()]; !ok {
			continue
		}

		sandboxes = append(sandboxes, entry.sandbox)
	}

	return sandboxes
}

// GetByHostPort looks up a sandbox by its host IP address parsed from hostPort.
func (m *Map) GetByHostPort(hostPort string) (*Sandbox, error) {
	reqIP, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, fmt.Errorf("error parsing remote address %s: %w", hostPort, err)
	}

	sbx, ok := m.network.Get(reqIP)
	if !ok {
		return nil, errors.New("sandbox not found")
	}

	return sbx, nil
}

// AssignNetwork registers a sandbox's IP so it is findable by GetByHostPort.
func (m *Map) AssignNetwork(ctx context.Context, sbx *Sandbox) {
	ip := sbx.Slot.HostIPString()
	m.network.Insert(ip, sbx)

	logger.L().Info(ctx, "sandbox network map entry added",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(ip),
	)
}

func (m *Map) TrackLifecycle(ctx context.Context, sbx *Sandbox, state SandboxState) {
	m.lifecycles.Insert(sandboxLifecycleKey(sbx.Runtime.SandboxID, sbx.LifecycleID), newLifecycleEntry(sbx, state))

	logger.L().Info(ctx, "sandbox lifecycle tracked",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
		zap.String("state", string(state)),
	)
}

// MarkRunning makes the sandbox visible to Get/Items/Count and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
	if !m.live.InsertIfAbsent(sbx.Runtime.SandboxID, sbx) {
		return
	}

	m.TrackLifecycle(ctx, sbx, SandboxStateRunning)

	m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnInsert(ctx, sbx)
	})

	logger.L().Info(ctx, "adding sandbox to map",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithTemplateID(sbx.Runtime.TemplateID),
		logger.WithBuildID(sbx.Runtime.BuildID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
		logger.WithEnvdVersion(sbx.Config.Envd.Version),
		logger.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		logger.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
	)
}

// MarkStopping removes the sandbox from live queries (Get, Items, Count).
// Returns true if the sandbox was successfully removed.
func (m *Map) MarkStopping(ctx context.Context, sandboxID, lifecycleID string) bool {
	stopped := false
	m.markLifecycleState(sandboxID, lifecycleID, SandboxStateStopping)

	m.live.RemoveCb(sandboxID, func(_ string, sbx *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if sbx.LifecycleID != lifecycleID {
			return false
		}

		logger.L().Info(ctx, "marking sandbox as stopping",
			logger.WithSandboxID(sandboxID),
			logger.WithLifecycleID(lifecycleID),
			logger.WithSandboxIP(sbx.Slot.HostIPString()),
		)

		stopped = true

		return true
	})

	return stopped
}

func (m *Map) MarkStopped(ctx context.Context, sbx *Sandbox) {
	m.lifecycles.Remove(sandboxLifecycleKey(sbx.Runtime.SandboxID, sbx.LifecycleID))

	logger.L().Info(ctx, "sandbox lifecycle stopped",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)
}

func (m *Map) markLifecycleState(sandboxID, lifecycleID string, state SandboxState) {
	key := sandboxLifecycleKey(sandboxID, lifecycleID)
	entry, ok := m.lifecycles.Get(key)
	if !ok {
		return
	}

	entry.setState(state)
}

// NetworkReleased unregisters a sandbox's IP and notifies OnNetworkRelease
// subscribers after a successful removal.
func (m *Map) NetworkReleased(ctx context.Context, ip string) {
	var sbx *Sandbox
	removed := m.network.RemoveCb(ip, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		sbx = v

		return exists
	})

	if !removed {
		return
	}

	logger.L().Info(ctx, "sandbox network map entry removed",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(ip),
	)

	m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnNetworkRelease(ctx, sbx)
	})
}
