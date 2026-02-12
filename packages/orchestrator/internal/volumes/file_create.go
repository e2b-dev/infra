package volumes

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var ErrExpectedStart = errors.New("expected start message")

var ErrUnexpectedStart = errors.New("unexpected start message")

func (v *VolumeService) CreateFile(server orchestrator.VolumeService_CreateFileServer) error {
	req, err := server.Recv()
	if err != nil {
		return v.processError(err)
	}

	start := req.GetStart()
	if start == nil {
		return v.processError(ErrExpectedStart)
	}

	basePath, statusErr := v.buildVolumePath(start.GetVolumeType(), start.GetTeamId(), start.GetVolumeId())
	if statusErr != nil {
		return statusErr.Err()
	}

	fullPath := filepath.Join(basePath, start.GetPath())

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return v.processError(err)
	}

	perm := os.FileMode(start.GetPermissions())
	if perm == 0 {
		perm = 0o644
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return v.processError(err)
	}
	defer file.Close()

	for {
		req, err := server.Recv()
		if err != nil {
			return v.processError(err)
		}

		switch m := req.GetMessage().(type) {
		case *orchestrator.VolumeFileCreateRequest_Content:
			if _, err := file.Write(m.Content.GetContent()); err != nil {
				return v.processError(err)
			}

		case *orchestrator.VolumeFileCreateRequest_Finish:
			return server.SendAndClose(&orchestrator.VolumeFileCreateResponse{})

		default:
			return v.processError(ErrUnexpectedStart)
		}
	}
}
