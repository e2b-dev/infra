package proxy

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

const (
	trafficKeepaliveRequestTimeout = 10 * time.Second
)

type trafficKeepaliveManager struct {
	resumer SandboxLifecycleClient
}

func newTrafficKeepaliveManager(resumer SandboxLifecycleClient) *trafficKeepaliveManager {
	return &trafficKeepaliveManager{
		resumer: resumer,
	}
}

func trafficKeepaliveEnabled(info *catalog.SandboxInfo) bool {
	if info == nil || info.TeamID == "" {
		return false
	}

	if info.Keepalive == nil {
		return false
	}

	return info.Keepalive.Traffic != nil && info.Keepalive.Traffic.Enabled
}

func (m *trafficKeepaliveManager) MaybeRefresh(ctx context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string, catalogStore catalog.SandboxesCatalog, info *catalog.SandboxInfo) {
	if m.resumer == nil || !trafficKeepaliveEnabled(info) {
		return
	}

	acquired, err := catalogStore.AcquireTrafficKeepalive(ctx, sandboxID)
	if err != nil {
		logger.L().Warn(ctx, "traffic keepalive acquire failed", logger.WithSandboxID(sandboxID), zap.Error(err))
		return
	}
	if !acquired {
		return
	}

	go func() {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficKeepaliveRequestTimeout)
		defer cancel()

		err := m.resumer.KeepAlive(refreshCtx, sandboxID, info.TeamID, sandboxPort, trafficAccessToken, envdAccessToken)
		if err != nil {
			logger.L().Warn(refreshCtx, "traffic keepalive refresh failed", logger.WithSandboxID(sandboxID), zap.Error(err))
		}
	}()
}
