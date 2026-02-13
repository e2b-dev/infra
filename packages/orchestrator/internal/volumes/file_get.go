package volumes

import (
	"io"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const fileStreamChunkSize = 1024 * 1024 // 1MB

func (v *VolumeService) GetFile(request *orchestrator.VolumeFileGetRequest, server orchestrator.VolumeService_GetFileServer) (err error) {
	defer func() {
		err = v.processError(err)
	}()

	fullPath, err := v.buildVolumePath(request.GetVolume(), request.GetPath())
	if err != nil {
		return err
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	if err := server.Send(&orchestrator.VolumeFileGetResponse{
		Message: &orchestrator.VolumeFileGetResponse_Start{Start: &orchestrator.VolumeFileGetResponseStart{Size: info.Size()}},
	}); err != nil {
		return err
	}

	buf := make([]byte, fileStreamChunkSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if err := server.Send(&orchestrator.VolumeFileGetResponse{
				Message: &orchestrator.VolumeFileGetResponse_Content{Content: &orchestrator.VolumeFileGetResponseContent{Content: buf[:n]}},
			}); err != nil {
				return err
			}
		}
		if err != nil {
			if err == io.EOF {
				return server.Send(&orchestrator.VolumeFileGetResponse{
					Message: &orchestrator.VolumeFileGetResponse_Finish{
						Finish: &orchestrator.VolumeFileGetResponseFinish{},
					},
				})
			}

			return err
		}
	}
}
