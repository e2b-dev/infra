package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// deadEvictionGracePeriod is how long a dead sandbox stays in the map so that
// IP-based lookups (logs, NFS, TCP firewall) still resolve while the
// Firecracker process finishes shutting down.
const deadEvictionGracePeriod = 30 * time.Second

type MapSubscriber interface {
	OnInsert(ctx context.Context, sandbox *Sandbox)
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
// It matches any sandbox in the map (starting, running, or dead).
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

	// Evict any stale entry that holds the same IP but a different sandbox ID.
	// This handles the edge case where a dead sandbox's eviction timer hasn't
	// fired yet when the network slot is recycled for a new sandbox.
	newIP := sbx.Slot.HostIPString()
	for id, existing := range m.sandboxes.Items() {
		if id == sbx.Runtime.SandboxID {
			continue
		}

		if existing.Slot.HostIPString() == newIP {
			logger.L().Info(ctx, "evicting stale sandbox with same IP on insert",
				logger.WithSandboxID(id),
				logger.WithSandboxIP(newIP),
			)
			m.sandboxes.Remove(id)
		}
	}

	m.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
}

// MarkRunning transitions a sandbox from starting to running and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
	sbx.status.Store(int32(StatusRunning))

	go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
		s.OnInsert(ctx, sbx)
	})
}

// MarkDead transitions a sandbox to the dead state. OnRemove subscribers are
// notified immediately (so the proxy / firewall limiter can clean up), but the
// entry stays in the map for deadEvictionGracePeriod so that IP-based lookups
// still resolve while the Firecracker process finishes shutting down.
func (m *Map) MarkDead(ctx context.Context, sandboxID string) {
	sbx, ok := m.sandboxes.Get(sandboxID)
	if !ok {
		return
	}

	// CAS ensures idempotency: if two goroutines race (e.g. concurrent
	// Delete RPCs for the same sandbox), only the winner fires OnRemove
	// and schedules the eviction timer.
	if !sbx.status.CompareAndSwap(int32(StatusRunning), int32(StatusDead)) {
		return
	}

	logger.L().Info(ctx, "marking sandbox as dead",
		logger.WithSandboxID(sandboxID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)

	go m.trigger(func(s MapSubscriber) {
		s.OnRemove(sandboxID)
	})

	// Schedule eviction after the grace period. evictDead uses a pointer
	// check so it won't remove a replacement sandbox inserted under the
	// same ID (e.g. checkpoint/resume).
	time.AfterFunc(deadEvictionGracePeriod, func() {
		m.evictDead(sandboxID, sbx)
	})
}

// evictDead removes a dead sandbox from the map after the grace period.
// It uses pointer equality to avoid removing a replacement sandbox that was
// inserted under the same ID (e.g. after checkpoint/resume).
func (m *Map) evictDead(sandboxID string, expected *Sandbox) {
	m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		return exists && v == expected
	})
}

func (m *Map) Remove(ctx context.Context, sandboxID string) {
	var removedSbx *Sandbox
	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, sbx *Sandbox, exists bool) bool {
		removedSbx = sbx

		return exists
	})

	if removed {
		logger.L().Info(ctx, "removing sandbox from map", logger.WithSandboxID(sandboxID))

		go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
			s.OnRemove(ctx, removedSbx)
		})
	}
}

func (m *Map) RemoveByLifecycleID(ctx context.Context, sandboxID, lifecycleID string) {
	var sbx *Sandbox
	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if v == nil {
			return false
		}

		sbx = v

		return v.LifecycleID == lifecycleID
	})

	if removed {
		logger.L().Info(ctx, "removing sandbox from map by lifecycle ID",
			logger.WithSandboxID(sandboxID),
			logger.WithSandboxIP(sbx.Slot.HostIPString()),
		)

		go m.trigger(ctx, func(ctx context.Context, s MapSubscriber) {
			s.OnRemove(ctx, sbx)
		})
	}
}

func NewSandboxesMap() *Map {
	return &Map{sandboxes: smap.New[*Sandbox]()}
}
