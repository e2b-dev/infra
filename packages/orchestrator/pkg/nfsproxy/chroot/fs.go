package chroot

import (
	"context"
	"os"

	"github.com/go-git/go-billy/v5"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/quota"
)

type wrappedFS struct {
	chroot *chrooted.Chrooted

	// Quota tracking (optional - nil tracker means disabled)
	ctx     context.Context //nolint:containedctx
	tracker *quota.Tracker
	volume  quota.VolumeInfo
}

func (f *wrappedFS) Create(filename string) (billy.File, error) {
	// Check quota before creating (new file uses space)
	if f.tracker != nil && f.tracker.IsBlocked(f.ctx, f.volume) {
		return nil, ErrQuotaExceeded
	}

	result, err := f.chroot.Create(filename)

	return maybeWrap(result, f.ctx, f.tracker, f.volume), err
}

func (f *wrappedFS) Open(filename string) (billy.File, error) {
	result, err := f.chroot.Open(filename)

	return maybeWrap(result, f.ctx, f.tracker, f.volume), err
}

func (f *wrappedFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	// Check quota before opening for write
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND) != 0 {
		if f.tracker != nil && f.tracker.IsBlocked(f.ctx, f.volume) {
			return nil, ErrQuotaExceeded
		}
	}

	result, err := f.chroot.OpenFile(filename, flag, perm)

	return maybeWrap(result, f.ctx, f.tracker, f.volume), err
}

func (f *wrappedFS) Stat(filename string) (os.FileInfo, error) {
	return f.chroot.Stat(filename)
}

func (f *wrappedFS) Rename(oldpath, newpath string) error {
	return f.chroot.Rename(oldpath, newpath)
}

func (f *wrappedFS) Remove(filename string) error {
	err := f.chroot.Remove(filename)

	// Mark dirty after successful removal (frees space)
	if err == nil && f.tracker != nil {
		f.tracker.MarkDirty(f.ctx, f.volume)
	}

	return err
}

func (f *wrappedFS) Join(elem ...string) string {
	return f.chroot.Join(elem...)
}

func (f *wrappedFS) TempFile(dir, prefix string) (billy.File, error) {
	// Check quota before creating temp file
	if f.tracker != nil && f.tracker.IsBlocked(f.ctx, f.volume) {
		return nil, ErrQuotaExceeded
	}

	result, err := f.chroot.TempFile(dir, prefix)

	return maybeWrap(result, f.ctx, f.tracker, f.volume), err
}

func (f *wrappedFS) ReadDir(path string) ([]os.FileInfo, error) {
	return f.chroot.ReadDir(path)
}

func (f *wrappedFS) MkdirAll(filename string, perm os.FileMode) error {
	return f.chroot.MkdirAll(filename, perm)
}

func (f *wrappedFS) Lstat(filename string) (os.FileInfo, error) {
	return f.chroot.Lstat(filename)
}

func (f *wrappedFS) Symlink(target, link string) error {
	return f.chroot.Symlink(target, link)
}

func (f *wrappedFS) Readlink(link string) (string, error) {
	return f.chroot.Readlink(link)
}

func (f *wrappedFS) Chroot(_ string) (billy.Filesystem, error) {
	return nil, os.ErrPermission
}

func (f *wrappedFS) Root() string {
	return f.chroot.Root()
}

var _ billy.Filesystem = (*wrappedFS)(nil)

func wrapChrooted(chroot *chrooted.Chrooted, ctx context.Context, tracker *quota.Tracker, volume quota.VolumeInfo) *wrappedFS {
	return &wrappedFS{
		chroot:  chroot,
		ctx:     ctx,
		tracker: tracker,
		volume:  volume,
	}
}
