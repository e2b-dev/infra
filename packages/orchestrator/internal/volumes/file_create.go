package volumes

import (
	"errors"
	"os"

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

	fullPath, err := v.buildVolumePath(start.GetVolume(), start.GetPath())
	if err != nil {
		return err
	}

	perm := os.FileMode(start.GetMode())

	flags := os.O_CREATE | os.O_WRONLY
	if !start.GetForce() { // do not overwrite an existing file
		flags |= os.O_EXCL | os.O_TRUNC
	}
	file, err := os.OpenFile(fullPath, flags, perm)
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
			if err = file.Sync(); err != nil {
				return err
			}

			if err := os.Chown(fullPath, int(start.GetOwnerId()), int(start.GetGroupId())); err != nil {
				return err
			}

			entry, err := os.Stat(fullPath)
			if err != nil {
				return err
			}

			return server.SendAndClose(&orchestrator.VolumeFileCreateResponse{
				Entry: toEntry(entry),
			})

		default:
			return ErrUnexpectedStart
		}
	}
}
