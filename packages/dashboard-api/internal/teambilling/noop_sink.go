package teambilling

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type NoopProvisionSink struct{}

func NewNoopProvisionSink() *NoopProvisionSink {
	return &NoopProvisionSink{}
}

func (s *NoopProvisionSink) ProvisionTeam(context.Context, teamprovision.TeamBillingProvisionRequestedV1) error {
	return nil
}
