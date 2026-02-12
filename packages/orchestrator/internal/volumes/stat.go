package volumes

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Stat(ctx context.Context, request *orchestrator.StatRequest) (*orchestrator.StatResponse, error) {
	// TODO implement me
	panic("implement me")
}
