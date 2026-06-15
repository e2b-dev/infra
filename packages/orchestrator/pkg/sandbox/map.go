//go:build linux

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

// Map tracks sandboxes in three indexes, managed independently:
//
//   - live: keyed by sandboxID, holds the current routable lifecycle per
//     sandbox from MarkRunning until MarkStopping. It serves the API/proxy
//     lookup paths (Get, Items, Count).
//   - lifecycles: keyed by sandboxID/lifecycleID, holds every lifecycle whose
//     cleanup is still outstanding, from MarkRunning until MarkStopped in
//     Close. During checkpoint/resume an old lifecycle can still be cleaning
//     up while a new lifecycle with the same sandboxID is already live, so a
//     sandboxID can map to multiple lifecycle entries. Shutdown uses this set
//     (WaitLifecycles, LifecycleItems) to wait for cleanup to finish, not
//     just for sandboxes to stop being routable.
//   - network: an IP-to-sandbox index managed by AssignNetwork and
//     NetworkReleased, serving GetByHostPort lookups.
//
// Invariant: live is a subset of lifecycles; MarkRunning inserts into both.
// The live and lifecycles maps could later be merged into a single registry
// keyed by sandboxID/lifecycleID with a running/stopping state per entry;
// they are kept separate for now to stay close to the pre-existing live-map
// shape.
type Map struct {
	live       *smap.Map[*Sandbox]
	lifecycles *smap.Map[*Sandbox]
	network    *smap.Map[*Sandbox]

	lifecycleMu      sync.Mutex
	lifecycleChanged chan struct{}

	subs     []MapSubscriber
	subsLock sync.RWMutex
}

func NewSandboxesMap() *Map {
	return &Map{
		live:             smap.New[*Sandbox](),
		lifecycles:       smap.New[*Sandbox](),
		network:          smap.New[*Sandbox](),
		lifecycleChanged: make(chan struct{}),
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
	items := m.lifecycles.Items()
	sandboxes := make([]*Sandbox, 0, len(items))
	for _, sbx := range items {
		sandboxes = append(sandboxes, sbx)
	}

	return sandboxes
}

func (m *Map) WaitLifecycles(ctx context.Context) error {
	for {
		m.lifecycleMu.Lock()
		if m.lifecycles.Count() == 0 {
			m.lifecycleMu.Unlock()

			return nil
		}

		changed := m.lifecycleChanged
		m.lifecycleMu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for sandbox lifecycle cleanup: %w", ctx.Err())
		case <-changed:
		}
	}
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

func (m *Map) trackLifecycle(ctx context.Context, sbx *Sandbox) {
	m.lifecycleMu.Lock()
	m.lifecycles.Insert(sandboxLifecycleKey(sbx.Runtime.SandboxID, sbx.LifecycleID), sbx)
	m.notifyLifecycleChangeLocked()
	m.lifecycleMu.Unlock()

	logger.L().Info(ctx, "sandbox lifecycle tracked",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)
}

// MarkRunning makes the sandbox visible to Get/Items/Count and notifies OnInsert subscribers.
func (m *Map) MarkRunning(ctx context.Context, sbx *Sandbox) {
	if !m.live.InsertIfAbsent(sbx.Runtime.SandboxID, sbx) {
		return
	}

	m.trackLifecycle(ctx, sbx)

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

func (m *Map) MarkStopped(ctx context.Context, sbx *Sandbox) {
	m.lifecycleMu.Lock()
	m.lifecycles.Remove(sandboxLifecycleKey(sbx.Runtime.SandboxID, sbx.LifecycleID))
	m.notifyLifecycleChangeLocked()
	m.lifecycleMu.Unlock()

	logger.L().Info(ctx, "sandbox lifecycle stopped",
		logger.WithSandboxID(sbx.Runtime.SandboxID),
		logger.WithLifecycleID(sbx.LifecycleID),
		logger.WithSandboxIP(sbx.Slot.HostIPString()),
	)
}

func (m *Map) notifyLifecycleChangeLocked() {
	close(m.lifecycleChanged)
	m.lifecycleChanged = make(chan struct{})
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
