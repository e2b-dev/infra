package teamprovision

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
)

type TeamProvisionSink interface {
	ProvisionTeam(ctx context.Context, req sharedteamprovision.TeamBillingProvisionRequestedV1) error
}

const (
	provisionSinkHTTP = "http"
	provisionSinkNoop = "noop"
)

type ProvisionError struct {
	StatusCode int
	Message    string
}

func (e *ProvisionError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return fmt.Sprintf("billing provisioning failed with status %d", e.StatusCode)
}

func (e *ProvisionError) IsBadRequest() bool {
	return e.StatusCode == 400
}

func provisionLogFields(req sharedteamprovision.TeamBillingProvisionRequestedV1, sink string) []zap.Field {
	return []zap.Field{
		logger.WithTeamID(req.TeamID.String()),
		logger.WithUserID(req.OwnerUserID.String()),
		zap.String("team.provision.reason", req.Reason),
		zap.String("team.provision.sink", sink),
	}
}

func provisionTelemetryAttrs(req sharedteamprovision.TeamBillingProvisionRequestedV1, sink string, attrs ...attribute.KeyValue) []attribute.KeyValue {
	base := []attribute.KeyValue{
		telemetry.WithTeamID(req.TeamID.String()),
		telemetry.WithUserID(req.OwnerUserID.String()),
		attribute.String("team.provision.reason", req.Reason),
		attribute.String("team.provision.sink", sink),
	}

	return append(base, attrs...)
}
