package volumes

import (
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const fileStreamChunkSize = 1024 * 1024 // 1MB

func (s *Service) GetFile(request *orchestrator.GetFileRequest, server orchestrator.VolumeService_GetFileServer) (err error) {
	ctx, span := tracer.Start(server.Context(), "get file from volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	fs, path, errResponse := s.getFilesystemAndPath(ctx, request)
	if errResponse != nil {
		return errResponse.Err()
	}
	defer fs.Close()

	span.AddEvent("opening file", trace.WithAttributes(
		attribute.String("path", path),
	))

	f, err := fs.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	span.AddEvent("getting file info")
	info, err := fs.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	span.AddEvent("sending file start", trace.WithAttributes(
		attribute.Int64("size", info.Size()),
	))
	if err := server.Send(&orchestrator.GetFileResponse{
		Message: &orchestrator.GetFileResponse_Start{
			Start: &orchestrator.VolumeFileGetResponseStart{
				Size: info.Size(),
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to send file start: %w", err)
	}

	buf := make([]byte, fileStreamChunkSize)
	for {
		span.AddEvent("reading file chunk")
		n, err := f.Read(buf)
		if n > 0 {
			span.AddEvent("send file chunk", trace.WithAttributes(
				attribute.Int("size", n),
			))
			if err := server.Send(&orchestrator.GetFileResponse{
				Message: &orchestrator.GetFileResponse_Content{
					Content: &orchestrator.VolumeFileGetResponseContent{
						Content: buf[:n],
					},
				},
			}); err != nil {
				return fmt.Errorf("failed to send file content: %w", err)
			}
		}
		if err == nil {
			// go grab another chunk
			continue
		}

		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read file: %w", err)
		}

		span.AddEvent("file read complete")

		break
	}

	if err := server.Send(&orchestrator.GetFileResponse{
		Message: &orchestrator.GetFileResponse_Finish{
			Finish: &orchestrator.VolumeFileGetResponseFinish{},
		},
	}); err != nil {
		return fmt.Errorf("failed to send file finish: %w", err)
	}

	return nil
}
