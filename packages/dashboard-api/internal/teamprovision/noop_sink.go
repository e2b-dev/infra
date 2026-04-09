package teamprovision

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type NoopProvisionSink struct{}

var _ TeamProvisionSink = (*NoopProvisionSink)(nil)

func NewNoopProvisionSink() *NoopProvisionSink {
	return &NoopProvisionSink{}
}

func (s *NoopProvisionSink) ProvisionTeam(ctx context.Context, req sharedteamprovision.TeamBillingProvisionRequestedV1) error {
	attrs := provisionTelemetryAttrs(req, provisionSinkNoop,
		attribute.String("team.provision.result", "skipped"),
	)
	telemetry.SetAttributes(ctx, attrs...)
	telemetry.ReportEvent(ctx, "team_provision.skipped", attrs...)

	fields := append(provisionLogFields(req, provisionSinkNoop),
		zap.String("team.provision.result", "skipped"),
	)
	logger.L().Info(ctx, "team provisioning skipped", fields...)

	return nil
}
