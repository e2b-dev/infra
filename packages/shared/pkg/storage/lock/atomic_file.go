package lock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type AtomicImmutableFile struct {
	lockFile *os.File
	tempFile *os.File
	filename string

	closeOnce sync.Once
}

func (f *AtomicImmutableFile) WriteAt(p []byte, offset int64) (n int, err error) {
	return f.tempFile.WriteAt(p, offset)
}

var _ io.WriterAt = (*AtomicImmutableFile)(nil)

func OpenFile(ctx context.Context, filename string) (*AtomicImmutableFile, error) {
	lockFile, err := TryAcquireLock(ctx, filename)
	if err != nil {
		return nil, err
	}

	tempFilename := fmt.Sprintf("%s.temp.%s", filename, uuid.NewString())
	tempFile, err := os.OpenFile(tempFilename, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		utils.Cleanup(ctx, "failed to release lock",
			func() error { return ReleaseLock(ctx, lockFile) })

		return nil, fmt.Errorf("failed to open temp file: %w", err)
	}

	return &AtomicImmutableFile{
		lockFile: lockFile,
		tempFile: tempFile,
		filename: filename,
	}, nil
}

func (f *AtomicImmutableFile) Commit(ctx context.Context) error {
	return f.close(ctx, true)
}

func (f *AtomicImmutableFile) Close(ctx context.Context) error {
	return f.close(ctx, false)
}

func (f *AtomicImmutableFile) close(ctx context.Context, success bool) error {
	var err error

	f.closeOnce.Do(func() {
		var errs []error

		defer utils.Cleanup(ctx, "failed to unlock file", func() error {
			return ReleaseLock(ctx, f.lockFile)
		})

		if err = f.tempFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close temp file: %w", err))
		}

		if success {
			if err = utils.RenameOrDeleteFile(ctx, f.tempFile.Name(), f.filename); err != nil {
				// someone else may have written the file successfully
				if !errors.Is(err, syscall.EEXIST) {
					// if not, report the error
					errs = append(errs, fmt.Errorf("failed to commit file: %w", err))
				}
			}
		} else {
			errs = append(errs, os.Remove(f.tempFile.Name()))
		}

		err = errors.Join(errs...)
	})

	return err
}
