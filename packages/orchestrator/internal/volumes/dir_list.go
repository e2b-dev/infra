package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func (v *VolumeService) ListDir(_ context.Context, request *orchestrator.VolumeDirListRequest) (r *orchestrator.VolumeDirListResponse, err error) {
	defer func() {
		err = v.processError(err)
	}()

	fullPath, err := v.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, err
	}

	items, err := os.ReadDir(fullPath)
	if err != nil { // todo: better error handling
		return nil, fmt.Errorf("failed to read directory %q: %w", fullPath, err)
	}

	var results []*orchestrator.VolumeDirectoryItem
	for _, item := range items {
		info, err := item.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to get info for item %q: %w", item.Name(), err)
		}

		results = append(results, &orchestrator.VolumeDirectoryItem{
			Entry: toEntry(info),
		})
	}

	return &orchestrator.VolumeDirListResponse{Files: results}, nil
}
