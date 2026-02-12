package volumes

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type makeDir func(path string, perm os.FileMode) error

func (v *VolumeService) CreateDir(_ context.Context, request *orchestrator.VolumeDirCreateRequest) (*orchestrator.VolumeDirCreateResponse, error) {
	path, statusErr := v.buildVolumePath(request.GetVolume())
	if statusErr != nil {
		return nil, statusErr.Err()
	}

	var fn makeDir
	if request.GetCreateParents() {
		fn = os.MkdirAll
	} else {
		fn = os.Mkdir
	}
	if err := fn(path, os.FileMode(request.GetMode())); err != nil { // todo: better error handling
		return nil, v.processError(err)
	}

	if err := os.Chown(path, int(request.GetOwnerId()), int(request.GetGroupId())); err != nil {
		return nil, v.processError(err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		return nil, v.processError(err)
	}

	return &orchestrator.VolumeDirCreateResponse{
		Entry: toEntry(stat),
	}, nil
}
