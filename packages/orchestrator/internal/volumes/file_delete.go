package volumes

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) DeleteFile(ctx context.Context, request *orchestrator.VolumeFileDeleteRequest) (*orchestrator.VolumeFileDeleteResponse, error) {
	// TODO implement me
	panic("implement me")
}
