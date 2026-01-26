package gcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/go-git/go-billy/v5"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	MetadataPermsAttr = "e2b-perms"
	MetadataUIDAttr   = "e2b-uid"
	MetadataGIDAttr   = "e2b-gid"
	MetadataATimeAttr = "e2b-atime"
	MetadataMTimeAttr = "e2b-mtime"
)

type BucketFS struct {
	bucket *storage.BucketHandle
}

func (p BucketFS) String() string {
	return fmt.Sprintf("BucketFS{bucket=%s}", p.bucket.BucketName())
}

var _ billy.Filesystem = (*BucketFS)(nil)

func NewGCSBucket(bucket *storage.BucketHandle) *BucketFS {
	return &BucketFS{bucket: bucket}
}

func (p BucketFS) Symlink(_, _ string) error {
	return fmt.Errorf("BucketFS.Symlink: %w", ErrUnsupported)
}

func (p BucketFS) Readlink(_ string) (string, error) {
	return "", fmt.Errorf("BucketFS.Readlink: %w", ErrUnsupported)
}

func (p BucketFS) Chroot(_ string) (billy.Filesystem, error) {
	return nil, fmt.Errorf("BucketFS.Chroot: %w", ErrUnsupported)
}

func (p BucketFS) Root() string {
	return ""
}

func (p BucketFS) Create(filename string) (billy.File, error) {
	return p.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

func (p BucketFS) Open(filename string) (billy.File, error) {
	return p.OpenFile(filename, os.O_RDONLY, 0)
}

func (p BucketFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	ctx := context.Background()

	// GCS does not allow you to seek+write
	// GCS does not allow you to append
	// GCS *always* truncates when writing

	if flag&os.O_CREATE != 0 && flag&os.O_TRUNC == 0 {
		logger.L().Warn(ctx, "O_CREATE without O_TRUNC; truncating file without user consent!", zap.String("filename", filename))
	}

	if flag&os.O_WRONLY != 0 && flag&os.O_TRUNC == 0 {
		logger.L().Warn(ctx, "O_WRONLY without O_TRUNC; truncating file without user consent!", zap.String("filename", filename))
	}

	if flag&os.O_RDWR != 0 && flag&os.O_TRUNC == 0 {
		logger.L().Warn(ctx, "O_RDWR without O_TRUNC; truncating file without user consent!", zap.String("filename", filename))
	}

	obj := p.bucket.Object(filename)

	// get the file's attrs
	attrs, err := obj.Attrs(ctx)

	// the file exists
	if err == nil {
		// we demanded to create the file, but it already exists
		if flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
			// return an error
			return nil, os.ErrExist
		}

		return newGcsFile(p, filename, attrs), nil
	}

	// the file does not exist
	if flag&os.O_CREATE == 0 {
		// we do not want to create it
		return nil, os.ErrNotExist
	}

	// create it
	w := obj.NewWriter(ctx)
	if err := w.Close(); err != nil {
		return nil, translateError(err)
	}

	// set the attributes
	permKey, permVal := fromPermToObjectMetadata(perm)

	updates := storage.ObjectAttrsToUpdate{
		Metadata: map[string]string{
			permKey: permVal,
		},
	}
	if attrs, err = obj.Update(ctx, updates); err != nil {
		return nil, translateError(err)
	}

	return newGcsFile(p, filename, attrs), nil
}

func fromPermToObjectMetadata(perm os.FileMode) (string, string) {
	return MetadataPermsAttr, fmt.Sprintf("%03o", perm)
}

func fromMetadataToPerm(metadata map[string]string) os.FileMode {
	var perm os.FileMode

	if metadata != nil {
		if val, ok := metadata[MetadataPermsAttr]; ok {
			p, err := strconv.ParseUint(val, 8, 32)
			if err == nil {
				perm = os.FileMode(p)
			}
		}
	}

	return perm
}

func fromUIDtoMetadata(uid int) (string, string) {
	return MetadataUIDAttr, fromIDtoMetadata(uid)
}

func fromGIDtoMetadata(gid int) (string, string) {
	return MetadataGIDAttr, fromIDtoMetadata(gid)
}

func fromATimeToMetadata(atime time.Time) (string, string) {
	return MetadataATimeAttr, fromTimeToMetadata(atime)
}

func fromMTimeToMetadata(mtime time.Time) (string, string) {
	return MetadataMTimeAttr, fromTimeToMetadata(mtime)
}

func fromTimeToMetadata(mtime time.Time) string {
	return mtime.Format(time.RFC3339Nano)
}

func fromIDtoMetadata(uid int) string {
	return strconv.FormatUint(uint64(uid), 10)
}

const (
	defaultUID = 1000
	defaultGID = 1000
)

func fromMetadataToUID(metadata map[string]string) uint32 {
	return fromMetadataID(metadata, MetadataUIDAttr, defaultUID)
}

func fromMetadataToGID(metadata map[string]string) uint32 {
	return fromMetadataID(metadata, MetadataGIDAttr, defaultGID)
}

// fromMetadataID returns the value of the given key in the metadata map, or the default value if the key is not present.
// For security, it's important that we not return 0 as a default, as that means "root"
func fromMetadataID(metadata map[string]string, key string, defaultID uint32) uint32 {
	if metadata != nil {
		if val, ok := metadata[key]; ok {
			u, err := strconv.ParseUint(val, 10, 32)
			if err == nil {
				return uint32(u)
			}
		}
	}

	return defaultID
}

func (p BucketFS) Stat(filename string) (os.FileInfo, error) {
	return p.Lstat(filename)
}

func (p BucketFS) Rename(oldPath, newPath string) error {
	ctx := context.Background()

	src := p.bucket.Object(oldPath)
	dst := p.bucket.Object(newPath)

	if _, err := dst.CopierFrom(src).Run(ctx); err != nil {
		return fmt.Errorf("failed to rename gcs object: %w", err)
	}

	return src.Delete(ctx)
}

func (p BucketFS) Remove(filename string) error {
	ctx := context.Background()

	return p.bucket.Object(filename).Delete(ctx)
}

func (p BucketFS) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (p BucketFS) TempFile(_, _ string) (billy.File, error) {
	return nil, fmt.Errorf("BucketFS.TempFile: %w", ErrUnsupported)
}

func (p BucketFS) ReadDir(path string) ([]os.FileInfo, error) {
	ctx := context.Background()

	objects := p.bucket.Objects(ctx, &storage.Query{Prefix: path + "/"})

	var results []os.FileInfo
	for {
		object, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("error when iterating over template object: %w", err)
		}

		if filepath.Base(object.Name) == dirMagicFilename {
			continue
		}

		results = append(results, fileInfo{object})
	}

	return results, nil
}

const dirMagicFilename = ".__.dir.__."

var dirMagicContent = []byte("do not delete")

func (p BucketFS) MkdirAll(filename string, _ os.FileMode) error {
	ctx := context.Background()

	if filename == "" || filename == "/" {
		return nil
	}

	dirName := filepath.Join(filename, dirMagicFilename)

	w := p.bucket.Object(dirName).NewWriter(ctx)
	defer func() {
		if err := w.Close(); err != nil {
			logger.L().Warn(ctx, "failed to close dir marker", zap.Error(err))
		}
	}()

	n, err := w.Write(dirMagicContent)
	if err != nil {
		return translateError(err)
	}
	if n != len(dirMagicContent) {
		return fmt.Errorf("expected to write 0 bytes, got %d", n)
	}

	return nil
}

func (p BucketFS) Lstat(filename string) (os.FileInfo, error) {
	if filename == "" || filename == "/" {
		return rootDir{}, nil
	}

	if file, done, err := p.tryGetFile(filename); done {
		return file, err
	}

	if dir, done, err := p.tryGetDirList(filename); done {
		return dir, err
	}

	if dir, done, err := p.tryGetMagicDir(filename); done {
		return dir, err
	}

	return nil, fmt.Errorf("failed to lstat object %q: %w", filename, os.ErrNotExist)
}

func (p BucketFS) tryGetFile(filename string) (os.FileInfo, bool, error) {
	ctx := context.Background()

	attrs, err := p.bucket.Object(filename).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, false, nil
		}

		return nil, true, fmt.Errorf("failed to read attrs for object %q: %w", filename, err)
	}

	return fileInfo{attrs}, true, nil
}

func (p BucketFS) tryGetDirList(filename string) (os.FileInfo, bool, error) {
	ctx := context.Background()

	results := p.bucket.Objects(ctx, &storage.Query{Prefix: filename + "/"})

	_, err := results.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			// couldn't find it, but that's not the end
			return nil, false, nil
		}

		return nil, true, fmt.Errorf("failed to list objects in %q: %w", filename, err)
	}

	// a nested file was found, which means the requested path is a directory
	return impliedDirInfo{filename}, true, nil
}

func (p BucketFS) tryGetMagicDir(filename string) (os.FileInfo, bool, error) {
	ctx := context.Background()

	dirName := filepath.Join(filename, dirMagicFilename)

	attrs, err := p.bucket.Object(dirName).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, false, nil
		}

		return nil, true, fmt.Errorf("failed to stat magic dir %q: %w", dirName, err)
	}

	return dirInfo{attrs}, true, nil
}
