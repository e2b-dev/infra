package chroot

import (
	"context"
	"errors"
	"os"

	"github.com/go-git/go-billy/v5"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/quota"
)

// ErrQuotaExceeded is returned when a write is blocked due to quota exceeded.
var ErrQuotaExceeded = errors.New("quota exceeded")

type wrappedFile struct {
	file *os.File

	// Quota tracking (optional - nil means disabled)
	tracker *quota.Tracker
	volume  quota.VolumeInfo
	ctx     context.Context //nolint:containedctx
}

var _ billy.File = (*wrappedFile)(nil)

func (w *wrappedFile) Name() string {
	return w.file.Name()
}

func (w *wrappedFile) Write(p []byte) (n int, err error) {
	// Check if blocked before writing
	if w.tracker != nil && w.tracker.IsBlocked(w.ctx, w.volume) {
		return 0, ErrQuotaExceeded
	}

	n, err = w.file.Write(p)

	// Mark volume as dirty after successful write
	if err == nil && n > 0 && w.tracker != nil {
		w.tracker.MarkDirty(w.ctx, w.volume)
	}

	return n, err
}

func (w *wrappedFile) Read(p []byte) (n int, err error) {
	return w.file.Read(p)
}

func (w *wrappedFile) ReadAt(p []byte, off int64) (n int, err error) {
	return w.file.ReadAt(p, off)
}

func (w *wrappedFile) Seek(offset int64, whence int) (int64, error) {
	return w.file.Seek(offset, whence)
}

func (w *wrappedFile) Close() error {
	return w.file.Close()
}

func (w *wrappedFile) Lock() error {
	return unix.Flock(int(w.file.Fd()), unix.LOCK_EX)
}

func (w *wrappedFile) Unlock() error {
	return unix.Flock(int(w.file.Fd()), unix.LOCK_UN)
}

func (w *wrappedFile) Truncate(size int64) error {
	// Check if blocked before truncating (truncate could increase size)
	if w.tracker != nil && w.tracker.IsBlocked(w.ctx, w.volume) {
		return ErrQuotaExceeded
	}

	err := w.file.Truncate(size)

	// Mark volume as dirty after successful truncate
	if err == nil && w.tracker != nil {
		w.tracker.MarkDirty(w.ctx, w.volume)
	}

	return err
}

func maybeWrap(f *os.File, ctx context.Context, tracker *quota.Tracker, volume quota.VolumeInfo) billy.File {
	if f == nil {
		return nil
	}

	return &wrappedFile{
		file:    f,
		tracker: tracker,
		volume:  volume,
		ctx:     ctx,
	}
}
