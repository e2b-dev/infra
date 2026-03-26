package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// SandboxStatus represents the lifecycle state of a sandbox.
type SandboxStatus int32

const (
	// StatusStarting is the initial state: the sandbox is being created but
	// is not yet ready to serve traffic.
	StatusStarting SandboxStatus = 0
	// StatusRunning means the sandbox is fully initialised and healthy.
	StatusRunning SandboxStatus = 1
	// StatusStopping means the sandbox has been killed or paused. It remains in
	// the map briefly so that IP-based lookups (logs, NFS, TCP firewall)
	// still resolve, but it is excluded from all "live" queries.
	StatusStopping SandboxStatus = 2
)

type MapSubscriber interface {
	// OnInsert is triggered when a sandbox transitions to the running state
	OnInsert(ctx context.Context, sandbox *Sandbox)
	// OnRemove is triggered when a sandbox is completely stopped and remove from the map
	OnRemove(ctx context.Context, sandbox *Sandbox)
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

func (m *Map) trigger(ctx context.Context, fn func(context.Context, MapSubscriber)) {
	m.subsLock.RLock()
	defer m.subsLock.RUnlock()

	for _, subscriber := range m.subs {
		fn(ctx, subscriber)
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
// It matches any sandbox in the map (starting, running, or stopping).
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
		logger.WithEnvdVersion(sbx.Config.Envd.Version),
		logger.WithKernelVersion(sbx.Config.FirecrackerConfig.KernelVersion),
		logger.WithFirecrackerVersion(sbx.Config.FirecrackerConfig.FirecrackerVersion),
	)

	m.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
}

// MarkRunning transitions a sandbox from starting to running and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
	if !sbx.status.CompareAndSwap(int32(StatusStarting), int32(StatusRunning)) {
		return
	}

	go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnInsert(ctx, sbx)
	})
}

// MarkStopping transitions a sandbox to the stopping state. It stays in in the map
// so that IP-based lookups still resolve while the Firecracker process finishes shutting down.
func (m *Map) MarkStopping(ctx context.Context, sandboxID, lifecycleID string) {
	// Use RemoveCb to update the sandbox atomically
	m.sandboxes.RemoveCb(sandboxID, func(_ string, sbx *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if sbx.LifecycleID != lifecycleID {
			return false
		}

		// It was already marked as stopping, no need to log again
		if !sbx.status.CompareAndSwap(int32(StatusRunning), int32(StatusStopping)) {
			return false
		}

		logger.L().Info(ctx, "marking sandbox as stopping by lifecycle ID",
			logger.WithSandboxID(sandboxID),
			logger.WithLifecycleID(lifecycleID),
			logger.WithSandboxIP(sbx.Slot.HostIPString()),
		)

		return false
	})
}

func (m *Map) Remove(ctx context.Context, sandboxID, lifecycleID string) {
	var sbx *Sandbox

	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if v.LifecycleID != lifecycleID {
			return false
		}

		sbx = v

		logger.L().Info(ctx, "removing sandbox by lifecycle ID",
			logger.WithSandboxID(sandboxID),
			logger.WithLifecycleID(lifecycleID),
			logger.WithSandboxIP(sbx.Slot.HostIPString()),
		)

		return true
	})

	if removed {
		go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
			s.OnRemove(ctx, sbx)
		})
	}
}

func NewSandboxesMap() *Map {
	return &Map{sandboxes: smap.New[*Sandbox]()}
}
