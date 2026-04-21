package proxy

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

const (
	trafficKeepaliveRefreshBefore  = time.Minute
	trafficKeepaliveMinInterval    = 30 * time.Second
	trafficKeepaliveRequestTimeout = 10 * time.Second
)

type trafficKeepaliveState struct {
	inFlight    bool
	lastAttempt time.Time
}

type trafficKeepaliveManager struct {
	resumer PausedSandboxResumer
	now     func() time.Time

	mu     sync.Mutex
	states map[string]trafficKeepaliveState
}

func newTrafficKeepaliveManager(resumer PausedSandboxResumer) *trafficKeepaliveManager {
	return &trafficKeepaliveManager{
		resumer: resumer,
		now:     time.Now,
		states:  map[string]trafficKeepaliveState{},
	}
}

func (m *trafficKeepaliveManager) MaybeRefresh(ctx context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string, info *catalog.SandboxInfo) {
	if m == nil || m.resumer == nil || info == nil || !info.TrafficKeepalive || info.TeamID == "" || info.EndTime.IsZero() {
		return
	}

	now := m.now()
	timeLeft := info.EndTime.Sub(now)
	if timeLeft <= 0 || timeLeft > trafficKeepaliveRefreshBefore {
		return
	}

	if !m.tryBegin(sandboxID, now) {
		return
	}

	go func() {
		defer m.finish(sandboxID)

		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficKeepaliveRequestTimeout)
		defer cancel()

		err := m.resumer.KeepAlive(refreshCtx, sandboxID, info.TeamID, sandboxPort, trafficAccessToken, envdAccessToken)
		if err != nil {
			logger.L().Warn(refreshCtx, "traffic keepalive refresh failed", logger.WithSandboxID(sandboxID), zap.Error(err))
		}
	}()
}

func (m *trafficKeepaliveManager) tryBegin(sandboxID string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.states[sandboxID]
	if state.inFlight {
		return false
	}
	if !state.lastAttempt.IsZero() && now.Sub(state.lastAttempt) < trafficKeepaliveMinInterval {
		return false
	}

	state.inFlight = true
	state.lastAttempt = now
	m.states[sandboxID] = state

	return true
}

func (m *trafficKeepaliveManager) finish(sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.states[sandboxID]
	state.inFlight = false
	m.states[sandboxID] = state
}
