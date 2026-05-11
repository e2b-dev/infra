package proxy

import (
	"context"
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
	resumer SandboxLifecycleClient
}

func newTrafficKeepaliveManager(resumer SandboxLifecycleClient) *trafficKeepaliveManager {
	return &trafficKeepaliveManager{
		resumer: resumer,
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
	// was disabled after the routing catalog entry was written.
	case codes.InvalidArgument, codes.Unauthenticated, codes.PermissionDenied, codes.FailedPrecondition:
		return true
	default:
		return false
	}
}

func (m *trafficKeepaliveManager) MaybeRefresh(ctx context.Context, sandboxID string, sandboxPort uint64, trafficAccessToken string, envdAccessToken string, catalogStore sandboxroutingcatalog.SandboxesCatalog, info *sandboxroutingcatalog.SandboxInfo) {
	if m.resumer == nil {
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

	acquired, err := catalogStore.AcquireTrafficKeepalive(ctx, sandboxID)
	if err != nil {
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
				if releaseErr := catalogStore.ReleaseTrafficKeepalive(refreshCtx, sandboxID); releaseErr != nil {
					logger.L().Warn(refreshCtx, "traffic keepalive release failed", logger.WithSandboxID(sandboxID), zap.Error(releaseErr))
				}
			}
		} else {
			logger.L().Info(refreshCtx, "traffic keepalive refreshed sandbox", logger.WithSandboxID(sandboxID))
		}
	}()
}
