package lock

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"
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

func OpenFile(filename string) (*AtomicFile, error) {
	lockFile, err := TryAcquireLock(filename)
	if err != nil {
		return nil, err
	}

	tempFilename := fmt.Sprintf("%s.temp.%s", filename, uuid.NewString())
	tempFile, err := os.OpenFile(tempFilename, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		cleanup("failed to close lock file", lockFile.Close)

		return nil, fmt.Errorf("failed to open temp file: %w", err)
	}

	return &AtomicFile{
		lockFile: lockFile,
		tempFile: tempFile,
		filename: filename,
	}, nil
}

func (f *AtomicFile) Close() error {
	var err error

	f.closeOnce.Do(func() {
		defer cleanup("failed to unlock file", func() error {
			return ReleaseLock(f.lockFile)
		})

		if err := f.tempFile.Close(); err != nil {
			err = fmt.Errorf("failed to close temp file: %w", err)
			return
		}

		if err := moveWithoutReplace(f.tempFile.Name(), f.filename); err != nil {
			err = fmt.Errorf("failed to commit file: %w", err)
			return
		}
	})

	return err
}

func cleanup(msg string, fn func() error) {
	if err := fn(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
	}
}

// moveWithoutReplace tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func moveWithoutReplace(oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			zap.L().Warn("failed to remove existing file", zap.Error(err))
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
