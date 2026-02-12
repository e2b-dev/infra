package volumes

import (
	"context"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type removeFunc func(path string) error

func (v *VolumeService) DeleteDir(_ context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	path, err := v.buildVolumePath(request.GetVolume())
	if err != nil {
		return nil, err
	}

	var fn removeFunc
	if request.GetRecursive() {
		fn = os.RemoveAll
	} else {
		fn = os.Remove
	}

	if err := fn(path); err != nil { // todo: better error handling
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
