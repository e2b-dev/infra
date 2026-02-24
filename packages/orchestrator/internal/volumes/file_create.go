package volumes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var ErrExpectedStart = errors.New("expected start message")

var ErrUnexpectedStart = errors.New("unexpected start message")

func (s *Service) CreateFile(server orchestrator.VolumeService_CreateFileServer) (err error) {
	ctx, span := tracer.Start(server.Context(), "create file in volume")
	defer func() {
		setSpanStatus(span, err)
		span.End()
	}()

	req, err := server.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive start message: %w", err)
	}

	start := req.GetStart()
	if start == nil {
		return ErrExpectedStart
	}

	fullPath, err := s.buildVolumePath(start.GetVolume(), start.GetPath())
	if err != nil {
		return fmt.Errorf("failed to build volume path: %w", err)
	}

	uid := utils.DerefOrDefault(start.Uid, defaultOwnerID)    //nolint:protogetter
	gid := utils.DerefOrDefault(start.Gid, defaultGroupID)    //nolint:protogetter
	mode := utils.DerefOrDefault(start.Mode, defaultFileMode) //nolint:protogetter

	span.AddEvent("creating file", trace.WithAttributes(
		attribute.String("path", fullPath),
		attribute.Int64("uid", int64(uid)),
		attribute.Int64("gid", int64(gid)),
		attribute.Int64("mode", int64(mode)),
		attribute.Bool("force", start.GetForce()),
	))

	if start.GetForce() {
		dirName := filepath.Dir(fullPath)
		if err := os.MkdirAll(dirName, os.FileMode(defaultDirMode)); err != nil {
			return fmt.Errorf("failed to create parent directories: %w", err)
		}
	}

	var flags int
	if start.GetForce() {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else {
		flags = os.O_CREATE | os.O_WRONLY | os.O_EXCL
	}

	file, err := os.OpenFile(fullPath, flags, os.FileMode(mode).Perm())
	if err != nil {
		return fmt.Errorf("failed to open file for create: %w", err)
	}

	deleteFileOnError := true
	defer func() {
		closeErr := file.Close()
		if closeErr != nil {
			logger.L().Error(ctx, "failed to close file", zap.Error(closeErr))
		}

		if err != nil && deleteFileOnError {
			deleteErr := os.Remove(fullPath)
			if deleteErr != nil {
				logger.L().Error(ctx, "failed to delete file after error", zap.Error(deleteErr))
			}
		}
	}()

	for {
		req, err := server.Recv()
		if err != nil {
			return fmt.Errorf("failed to receive chunk: %w", err)
		}

		switch m := req.GetMessage().(type) {
		case *orchestrator.VolumeFileCreateRequest_Content:
			if _, err := file.Write(m.Content.GetContent()); err != nil {
				return fmt.Errorf("failed to write file content: %w", err)
			}

		case *orchestrator.VolumeFileCreateRequest_Finish:
			if err = file.Sync(); err != nil {
				return fmt.Errorf("failed to sync file to disk: %w", err)
			}

			if err := os.Chown(fullPath, int(uid), int(gid)); err != nil {
				return fmt.Errorf("failed to set file ownership: %w", err)
			}

			// we do this again to avoid the process' umask from automatically 'fixing' our requests.
			if err := os.Chmod(fullPath, os.FileMode(mode)); err != nil {
				return fmt.Errorf("failed to set file mode: %w", err)
			}

			deleteFileOnError = false

			entry, err := toEntryFromPath(fullPath, start.GetPath())
			if err != nil {
				return fmt.Errorf("failed to stat created file: %w", err)
			}

			return server.SendAndClose(&orchestrator.VolumeFileCreateResponse{
				Entry: entry,
			})

		default:
			return ErrUnexpectedStart
		}
	}
}
