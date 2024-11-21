package cache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

const (
	pageSize        = 2 << 11
	hugepageSize    = 2 << 20
	rootfsBlockSize = 2 << 11
)

type Template struct {
	files *storage.TemplateCacheFiles

	memfile  func() (block.ReadonlyDevice, error)
	rootfs   func() (block.ReadonlyDevice, error)
	snapfile func() (*File, error)

	rootfsResult   chan valueWithErr[block.ReadonlyDevice]
	memfileResult  chan valueWithErr[block.ReadonlyDevice]
	snapfileResult chan valueWithErr[*File]

	hugePages bool
}

type valueWithErr[T any] struct {
	value T
	err   error
}

func (t *TemplateCache) newTemplate(
	cacheIdentifier,
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) *Template {
	rootfsResult := make(chan valueWithErr[block.ReadonlyDevice], 1)
	memfileResult := make(chan valueWithErr[block.ReadonlyDevice], 1)
	snapfileResult := make(chan valueWithErr[*File], 1)

	h := &Template{
		rootfsResult:   rootfsResult,
		memfileResult:  memfileResult,
		snapfileResult: snapfileResult,
		hugePages:      hugePages,
		files: storage.NewTemplateCacheFiles(
			storage.NewTemplateFiles(
				templateId,
				buildId,
				kernelVersion,
				firecrackerVersion,
			),
			cacheIdentifier,
		),
		memfile: sync.OnceValues(func() (block.ReadonlyDevice, error) {
			result := <-memfileResult

			return result.value, result.err
		}),
		rootfs: sync.OnceValues(func() (block.ReadonlyDevice, error) {
			result := <-rootfsResult

			return result.value, result.err
		}),
		snapfile: sync.OnceValues(func() (*File, error) {
			result := <-snapfileResult

			return result.value, result.err
		}),
	}

	return h
}

func (t *Template) Fetch(ctx context.Context, bucket *gcs.BucketHandle) {
	err := os.MkdirAll(t.files.CacheDir(), os.ModePerm)
	if err != nil {
		errMsg := fmt.Errorf("failed to create directory %s: %w", t.files.CacheDir(), err)

		t.memfileResult <- valueWithErr[block.ReadonlyDevice]{
			err: errMsg,
		}

		t.rootfsResult <- valueWithErr[block.ReadonlyDevice]{
			err: errMsg,
		}

		t.snapfileResult <- valueWithErr[*File]{
			err: errMsg,
		}

		return
	}

	go func() {
		snapfile, snapfileErr := NewFile(ctx, bucket, t.files.StorageSnapfilePath(), t.files.CacheSnapfilePath())
		if snapfileErr != nil {
			t.snapfileResult <- valueWithErr[*File]{
				err: fmt.Errorf("failed to fetch snapfile: %w", snapfileErr),
			}

			return
		}

		t.snapfileResult <- valueWithErr[*File]{
			value: snapfile,
		}
	}()

	go func() {
		var memfileBlockSize int64
		if t.hugePages {
			memfileBlockSize = hugepageSize
		} else {
			memfileBlockSize = pageSize
		}

		memfileStorage, memfileErr := block.NewStorage(
			ctx,
			bucket,
			t.files.StorageMemfilePath(),
			memfileBlockSize,
			t.files.CacheMemfilePath(),
		)
		if memfileErr != nil {
			t.memfileResult <- valueWithErr[block.ReadonlyDevice]{
				err: fmt.Errorf("failed to create memfile storage: %w", memfileErr),
			}

			return
		}

		t.memfileResult <- valueWithErr[block.ReadonlyDevice]{
			value: memfileStorage,
		}
	}()

	go func() {
		rootfsStorage, rootfsErr := block.NewStorage(
			ctx,
			bucket,
			t.files.StorageRootfsPath(),
			// TODO: This should ideally be the blockSize (4096), but we would need to implement more complex dirty block caching in cache there.
			ChunkSize,
			t.files.CacheRootfsPath(),
		)
		if rootfsErr != nil {
			t.rootfsResult <- valueWithErr[block.ReadonlyDevice]{
				err: fmt.Errorf("failed to create rootfs storage: %w", rootfsErr),
			}

			return
		}

		t.rootfsResult <- valueWithErr[block.ReadonlyDevice]{
			value: rootfsStorage,
		}
	}()
}

func (t *Template) Close() error {
	var errs []error

	memfile, err := t.memfile()
	if err == nil {
		errs = append(errs, memfile.Close())
	}

	rootfs, err := t.rootfs()
	if err == nil {
		errs = append(errs, rootfs.Close())
	}

	snapfile, err := t.snapfile()
	if err == nil {
		errs = append(errs, snapfile.Close())
	}

	return errors.Join(errs...)
}

func (t *Template) Files() *storage.TemplateCacheFiles {
	return t.files
}

func (t *Template) Memfile() (block.ReadonlyDevice, error) {
	return t.memfile()
}

func (t *Template) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs()
}

func (t *Template) Snapfile() (*File, error) {
	return t.snapfile()
}
