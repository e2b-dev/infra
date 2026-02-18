package volumes

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type removeFunc func(path string) error

func (s *Service) DeleteDir(_ context.Context, request *orchestrator.VolumeDirDeleteRequest) (r *orchestrator.VolumeDirDeleteResponse, err error) {
	defer func() {
		err = s.processError(err)
	}()

	fullPath, err := s.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return nil, err
	}

	var fn removeFunc
	if request.GetRecursive() {
		fn = os.RemoveAll
	} else {
		fn = os.Remove
	}

	if err := fn(fullPath); err != nil { // todo: better error handling
		return nil, fmt.Errorf("failed to delete directory: %w", err)
	}

	return &orchestrator.VolumeDirDeleteResponse{}, nil
}
