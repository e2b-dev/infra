package teamprovision

import (
	"context"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type NoopProvisionSink struct{}

var _ TeamProvisionSink = (*NoopProvisionSink)(nil)

func NewNoopProvisionSink() *NoopProvisionSink {
	return &NoopProvisionSink{}
}

func (s *NoopProvisionSink) ProvisionTeam(context.Context, sharedteamprovision.TeamBillingProvisionRequestedV1) error {
	return nil
}
