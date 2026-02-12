package volumes

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var ErrExpectedStart = errors.New("expected start message")

var ErrUnexpectedStart = errors.New("unexpected start message")

func (v *VolumeService) CreateFile(server orchestrator.VolumeService_CreateFileServer) (err error) {
	defer func() {
		err = v.processError(err)
	}()

	req, err := server.Recv()
	if err != nil {
		return err
	}

	start := req.GetStart()
	if start == nil {
		return ErrExpectedStart
	}

	basePath, err := v.buildVolumePath(start.GetVolume())
	if err != nil {
		return err
	}

	fullPath := filepath.Join(basePath, start.GetPath())

	perm := os.FileMode(start.GetMode())

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer file.Close()

	for {
		req, err := server.Recv()
		if err != nil {
			return err
		}

		switch m := req.GetMessage().(type) {
		case *orchestrator.VolumeFileCreateRequest_Content:
			if _, err := file.Write(m.Content.GetContent()); err != nil {
				return err
			}

		case *orchestrator.VolumeFileCreateRequest_Finish:
			if err := os.Chown(fullPath, int(start.GetOwnerId()), int(start.GetGroupId())); err != nil {
				return err
			}

			return server.SendAndClose(&orchestrator.VolumeFileCreateResponse{})

		default:
			return ErrUnexpectedStart
		}
	}
}
