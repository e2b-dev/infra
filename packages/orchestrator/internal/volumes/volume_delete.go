package volumes

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) Delete(
	_ context.Context,
	request *orchestrator.VolumeDeleteRequest,
) (r *orchestrator.VolumeDeleteResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	volumePath, err := v.buildVolumePath(request.GetVolume())
	if err != nil {
		return nil, err
	}

	if err := os.RemoveAll(volumePath); err != nil {
		return nil, err
	}

	return &orchestrator.VolumeDeleteResponse{}, nil
}
