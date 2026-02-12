package volumes

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type makeDir func(path string, perm os.FileMode) error

func (v *VolumeService) CreateDir(_ context.Context, request *orchestrator.VolumeDirCreateRequest) (r *orchestrator.VolumeDirCreateResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	path, err := v.buildVolumePath(request.GetVolume())
	if err != nil {
		return nil, err
	}

	var fn makeDir
	if request.GetCreateParents() {
		fn = os.MkdirAll
	} else {
		fn = os.Mkdir
	}
	if err := fn(path, os.FileMode(request.GetMode())); err != nil { // todo: better error handling
		return nil, err
	}

	if err := os.Chown(path, int(request.GetOwnerId()), int(request.GetGroupId())); err != nil {
		return nil, err
	}

	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	return &orchestrator.VolumeDirCreateResponse{
		Entry: toEntry(stat),
	}, nil
}
