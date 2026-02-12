package volumes

import (
	"io"
	"os"
	"path/filepath"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const fileStreamChunkSize = 1024 * 1024 // 1MB

func (v *VolumeService) GetFile(request *orchestrator.VolumeFileGetRequest, server orchestrator.VolumeService_GetFileServer) error {
	basePath, statusErr := v.buildVolumePath(request.GetVolumeType(), request.GetTeamId(), request.GetVolumeId())
	if statusErr != nil {
		return statusErr.Err()
	}

	fullPath := filepath.Join(basePath, request.GetPath())

	f, err := os.Open(fullPath)
	if err != nil {
		return v.processError(err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return v.processError(err)
	}

	if err := server.Send(&orchestrator.VolumeFileGetResponse{
		Message: &orchestrator.VolumeFileGetResponse_Start{Start: &orchestrator.VolumeFileGetResponseStart{Size: info.Size()}},
	}); err != nil {
		return err
	}

	buf := make([]byte, fileStreamChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if err := server.Send(&orchestrator.VolumeFileGetResponse{
				Message: &orchestrator.VolumeFileGetResponse_Content{Content: &orchestrator.VolumeFileGetResponseContent{Content: buf[:n]}},
			}); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}

			return v.processError(readErr)
		}
	}

	if err := server.Send(&orchestrator.VolumeFileGetResponse{
		Message: &orchestrator.VolumeFileGetResponse_Finish{Finish: &orchestrator.VolumeFileGetResponseFinish{}},
	}); err != nil {
		return v.processError(err)
	}

	return nil
}
