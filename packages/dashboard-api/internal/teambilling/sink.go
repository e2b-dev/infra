package teambilling

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

type TeamProvisionSink interface {
	ProvisionTeam(ctx context.Context, req teamprovision.TeamBillingProvisionRequestedV1) error
}

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
