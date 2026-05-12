package proxy

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandboxroutingcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

const (
	trafficKeepaliveRequestTimeout = 10 * time.Second
)

type trafficKeepaliveManager struct {
	resumer      SandboxLifecycleClient
	catalogStore sandboxroutingcatalog.SandboxesCatalog
}

func newTrafficKeepaliveManager(resumer SandboxLifecycleClient, catalogStore sandboxroutingcatalog.SandboxesCatalog) *trafficKeepaliveManager {
	return &trafficKeepaliveManager{
		resumer:      resumer,
		catalogStore: catalogStore,
	}
}

func routingInfoHasTrafficKeepalive(info *sandboxroutingcatalog.SandboxInfo) bool {
	// Older catalog entries can have keepalive metadata without team_id until they expire.
	if info == nil || info.TeamID == "" {
		return false
	}

	return info.TrafficKeepalive
}

func releaseTrafficKeepaliveOnFailure(err error) bool {
	switch status.Code(err) {
	// FailedPrecondition includes API-side policy rejection, e.g. traffic keepalive
	// was disabled after the routing catalog entry was written. Auth and
	// validation errors keep the throttle held because immediate retries will
	// hit the same caller-side failure.
	case codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

func (m *trafficKeepaliveManager) MaybeRefresh(ctx context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string, info *sandboxroutingcatalog.SandboxInfo) {
	if m.resumer == nil || m.catalogStore == nil {
		return
	}
	if !routingInfoHasTrafficKeepalive(info) {
		logger.L().Debug(
			ctx,
			"traffic keepalive disabled in routing catalog",
			logger.WithSandboxID(sandboxID),
			zap.Bool("team_id_present", info != nil && info.TeamID != ""),
			zap.Bool("traffic_keepalive", info != nil && info.TrafficKeepalive),
		)

		return
	}

	acquired, err := m.catalogStore.AcquireTrafficKeepalive(ctx, sandboxID)
	if err != nil {
		// The memory catalog can expire between catalog resolution and throttle acquire.
		if errors.Is(err, sandboxroutingcatalog.ErrSandboxNotFound) {
			logger.L().Debug(ctx, "traffic keepalive catalog entry expired before acquire", logger.WithSandboxID(sandboxID))

			return
		}

		logger.L().Warn(ctx, "traffic keepalive acquire failed", logger.WithSandboxID(sandboxID), zap.Error(err))

		return
	}
	if !acquired {
		logger.L().Debug(ctx, "traffic keepalive refresh already acquired", logger.WithSandboxID(sandboxID))

		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.L().Error(ctx, "traffic keepalive refresh panicked", logger.WithSandboxID(sandboxID), zap.Any("panic", r))
			}
		}()

		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficKeepaliveRequestTimeout)
		defer cancel()

		err := m.resumer.KeepAlive(refreshCtx, sandboxID, info.TeamID, sandboxPort, trafficAccessToken, envdAccessToken)
		if err != nil {
			logger.L().Warn(refreshCtx, "traffic keepalive refresh failed", logger.WithSandboxID(sandboxID), zap.Error(err))
			if releaseTrafficKeepaliveOnFailure(err) {
				if releaseErr := m.catalogStore.ReleaseTrafficKeepalive(refreshCtx, sandboxID); releaseErr != nil {
					logger.L().Warn(refreshCtx, "traffic keepalive release failed", logger.WithSandboxID(sandboxID), zap.Error(releaseErr))
				}
			}
		} else {
			logger.L().Info(refreshCtx, "traffic keepalive refreshed sandbox", logger.WithSandboxID(sandboxID))
		}
	}()
}
