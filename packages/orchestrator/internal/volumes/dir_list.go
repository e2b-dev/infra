package volumes

import (
	"context"
	"os"

	"github.com/gogo/status"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) ListDir(_ context.Context, request *orchestrator.VolumeDirListRequest) (r *orchestrator.VolumeDirListResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	path, err := v.buildVolumePath(request.GetVolume())
	if err != nil {
		return nil, err
	}

	items, err := os.ReadDir(path)
	if err != nil { // todo: better error handling
		return nil, status.Error(codes.Internal, err.Error())
	}

	var results []*orchestrator.VolumeDirectoryItem
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		results = append(results, &orchestrator.VolumeDirectoryItem{
			Entry: toEntry(info),
		})
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}
