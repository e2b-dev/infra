package sbxlogger

import (
	"context"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type SandboxLogger struct {
	logger.Logger
}

type HealthCheckAction int

const (
	Success HealthCheckAction = iota
	Fail
	ReportSuccess
	ReportFail
)

func (sl *SandboxLogger) Healthcheck(ctx context.Context, action HealthCheckAction) {
	switch action {
	case Success:
		sl.Info(ctx, "Sandbox healthcheck recovered",
			zap.Bool("healthcheck", true))
	case Fail:
		sl.Error(ctx, "Sandbox healthcheck started failing",
			zap.Bool("healthcheck", false))
	case ReportSuccess:
		sl.Info(
			ctx,
			"Control sandbox healthcheck was successful",
			zap.Bool("healthcheck", true))
	case ReportFail:
		sl.Error(ctx, "Control sandbox healthcheck was unsuccessful",
			zap.Bool("healthcheck", false))
	}
}
