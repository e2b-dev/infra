package volumes

import (
	"fmt"
	"io"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const fileStreamChunkSize = 1024 * 1024 // 1MB

func (s *Service) GetFile(request *orchestrator.VolumeFileGetRequest, server orchestrator.VolumeService_GetFileServer) (err error) {
	_, span := tracer.Start(server.Context(), "get file from volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()
	paths, err := s.buildPaths(request)
	if err != nil {
		return fmt.Errorf("failed to build volume path: %w", err)
	}

	span.AddEvent("retrieving file", trace.WithAttributes(
		attribute.String("path", paths.HostFullPath),
	))

	f, err := os.Open(paths.HostFullPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if err := server.Send(&orchestrator.VolumeFileGetResponse{
		Message: &orchestrator.VolumeFileGetResponse_Start{Start: &orchestrator.VolumeFileGetResponseStart{Size: info.Size()}},
	}); err != nil {
		return fmt.Errorf("failed to send file start: %w", err)
	}

	buf := make([]byte, fileStreamChunkSize)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if err := server.Send(&orchestrator.VolumeFileGetResponse{
				Message: &orchestrator.VolumeFileGetResponse_Content{Content: &orchestrator.VolumeFileGetResponseContent{Content: buf[:n]}},
			}); err != nil {
				return fmt.Errorf("failed to send file content: %w", err)
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

			return fmt.Errorf("failed to read file: %w", err)
		}
	}
}
