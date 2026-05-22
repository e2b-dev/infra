package teamprovision

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
	Err        error
}

func (e *ProvisionError) Error() string {
	if e.Message != "" {
		return e.Message
	}

	return fmt.Sprintf("billing provisioning failed with status %d", e.StatusCode)
}

func (e *ProvisionError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

func provisionLogFields(req sharedteamprovision.TeamBillingProvisionRequestedV1, sink string) []zap.Field {
	return []zap.Field{
		logger.WithTeamID(req.TeamID.String()),
		logger.WithUserID(req.CreatorUserID.String()),
		zap.String("team.provision.reason", req.Reason),
		zap.String("team.provision.sink", sink),
	}
}

func provisionTelemetryAttrs(req sharedteamprovision.TeamBillingProvisionRequestedV1, sink string, attrs ...attribute.KeyValue) []attribute.KeyValue {
	base := []attribute.KeyValue{
		telemetry.WithTeamID(req.TeamID.String()),
		telemetry.WithUserID(req.CreatorUserID.String()),
		attribute.String("team.provision.reason", req.Reason),
		attribute.String("team.provision.sink", sink),
	}

	return append(base, attrs...)
}
