package volumes

import (
	"context"
	"os"

	"github.com/gogo/status"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) ListDir(ctx context.Context, request *orchestrator.VolumeListDirRequest) (*orchestrator.VolumeListDirResponse, error) {
	path, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return nil, statusErr.Err()
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
			Name:        item.Name(),
			IsDirectory: item.IsDir(),
			Mode:        uint32(info.Mode()),
			Size:        0,
			Ctime:       nil,
			Mtime:       nil,
		})
	}

	return &orchestrator.VolumeListDirResponse{Files: results}, nil
}
