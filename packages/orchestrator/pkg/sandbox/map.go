package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

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

// Map holds sandboxes that are live (running) together with a IP-to-sandbox index
// The two maps are managed independently.
//
// AssignNetwork/NetworkReleased manage the IP map,
// MarkRunning/MarkStopping manage the live set.
type Map struct {
	live    *smap.Map[*Sandbox]
	network *smap.Map[*Sandbox]

	subs     []MapSubscriber
	subsLock sync.RWMutex
}

func NewSandboxesMap() *Map {
	return &Map{
		live:    smap.New[*Sandbox](),
		network: smap.New[*Sandbox](),
	}
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

// MarkRunning makes the sandbox visible to Get/Items/Count and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
	if !m.live.InsertIfAbsent(sbx.Runtime.SandboxID, sbx) {
		return
	}

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
