package lock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type AtomicFile struct {
	lockFile *os.File
	tempFile *os.File
	filename string

	closeOnce sync.Once
}

func (f *AtomicFile) Write(p []byte) (n int, err error) {
	return f.tempFile.Write(p)
}

var _ io.Writer = (*AtomicFile)(nil)

func OpenFile(ctx context.Context, filename string) (*AtomicFile, error) {
	lockFile, err := TryAcquireLock(ctx, filename)
	if err != nil {
		return nil, err
	}

	tempFilename := fmt.Sprintf("%s.temp.%s", filename, uuid.NewString())
	tempFile, err := os.OpenFile(tempFilename, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		cleanup(ctx, "failed to release lock",
			func() error { return ReleaseLock(ctx, lockFile) })

		return nil, fmt.Errorf("failed to open temp file: %w", err)
	}

	return &AtomicFile{
		lockFile: lockFile,
		tempFile: tempFile,
		filename: filename,
	}, nil
}

func (f *AtomicFile) Close(ctx context.Context) error {
	var err error

	f.closeOnce.Do(func() {
		defer cleanup(ctx, "failed to unlock file", func() error {
			return ReleaseLock(ctx, f.lockFile)
		})

		if err = f.tempFile.Close(); err != nil {
			err = fmt.Errorf("failed to close temp file: %w", err)

			return
		}

		if err = moveWithoutReplace(ctx, f.tempFile.Name(), f.filename); err != nil {
			err = fmt.Errorf("failed to commit file: %w", err)

			if rmErr := os.Remove(f.tempFile.Name()); rmErr != nil {
				err = errors.Join(err, fmt.Errorf("failed to remove temp file: %w", rmErr))
			}

			return
		}
	})

	return err
}

func cleanup(ctx context.Context, msg string, fn func() error) {
	if err := fn(); err != nil {
		logger.L().Warn(ctx, msg, zap.Error(err))
	}
}

// moveWithoutReplace tries to rename a file but will not replace the target if it already exists.
// The old file is deleted if it can't be moved for any reason.
func moveWithoutReplace(ctx context.Context, oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			logger.L().Warn(ctx, "failed to remove existing file", zap.Error(err))
		}
	}()

	if err := os.Link(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Someone else created newPath first. Treat as success.
			return nil
		}

		return err
	}

	return nil
}
