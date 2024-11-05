package local_storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
)

const (
	pageSize        = 2 << 11
	hugepageSize    = 2 << 20
	rootfsBlockSize = 2 << 11
)

type Template struct {
	Files *templateStorage.TemplateCacheFiles

	Memfile  func() (*templateStorage.BlockStorage, error)
	Rootfs   func() (*templateStorage.BlockStorage, error)
	Snapfile func() (*File, error)

	rootfsResult   chan valueWithErr[*templateStorage.BlockStorage]
	memfileResult  chan valueWithErr[*templateStorage.BlockStorage]
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
	rootfsResult := make(chan valueWithErr[*templateStorage.BlockStorage], 1)
	memfileResult := make(chan valueWithErr[*templateStorage.BlockStorage], 1)
	snapfileResult := make(chan valueWithErr[*File], 1)

	h := &Template{
		rootfsResult:   rootfsResult,
		memfileResult:  memfileResult,
		snapfileResult: snapfileResult,
		hugePages:      hugePages,
		Files: templateStorage.NewTemplateCacheFiles(
			templateStorage.NewTemplateFiles(
				templateId,
				buildId,
				kernelVersion,
				firecrackerVersion,
			),
			cacheIdentifier,
		),
		Memfile: sync.OnceValues(func() (*templateStorage.BlockStorage, error) {
			result := <-memfileResult

			return result.value, result.err
		}),
		Rootfs: sync.OnceValues(func() (*templateStorage.BlockStorage, error) {
			result := <-rootfsResult

			return result.value, result.err
		}),
		Snapfile: sync.OnceValues(func() (*File, error) {
			result := <-snapfileResult

			return result.value, result.err
		}),
	}

	return h
}

func (t *Template) Fetch(ctx context.Context, bucket *storage.BucketHandle) {
	err := os.MkdirAll(t.Files.CacheDir(), os.ModePerm)
	if err != nil {
		errMsg := fmt.Errorf("failed to create directory %s: %w", t.Files.CacheDir(), err)

		t.memfileResult <- valueWithErr[*templateStorage.BlockStorage]{
			err: errMsg,
		}

		t.rootfsResult <- valueWithErr[*templateStorage.BlockStorage]{
			err: errMsg,
		}

		t.snapfileResult <- valueWithErr[*File]{
			err: errMsg,
		}

		return
	}

	go func() {
		snapfile, snapfileErr := NewFile(ctx, bucket, t.Files.StorageSnapfilePath(), t.Files.CacheSnapfilePath())
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

		memfileStorage, memfileErr := templateStorage.NewBlockStorage(
			ctx,
			bucket,
			t.Files.StorageMemfilePath(),
			memfileBlockSize,
			t.Files.CacheMemfilePath(),
		)
		if memfileErr != nil {
			t.memfileResult <- valueWithErr[*templateStorage.BlockStorage]{
				err: fmt.Errorf("failed to create memfile storage: %w", memfileErr),
			}

			return
		}

		t.memfileResult <- valueWithErr[*templateStorage.BlockStorage]{
			value: memfileStorage,
		}
	}()

	go func() {
		rootfsStorage, rootfsErr := templateStorage.NewBlockStorage(
			ctx,
			bucket,
			t.Files.StorageRootfsPath(),
			rootfsBlockSize,
			t.Files.CacheRootfsPath(),
		)
		if rootfsErr != nil {
			t.rootfsResult <- valueWithErr[*templateStorage.BlockStorage]{
				err: fmt.Errorf("failed to create rootfs storage: %w", rootfsErr),
			}

			return
		}

		t.rootfsResult <- valueWithErr[*templateStorage.BlockStorage]{
			value: rootfsStorage,
		}
	}()
}

func (t *Template) Close() error {
	var errs []error

	memfile, err := t.Memfile()
	if err == nil {
		errs = append(errs, memfile.Close())
	}

	rootfs, err := t.Rootfs()
	if err == nil {
		errs = append(errs, rootfs.Close())
	}

	snapfile, err := t.Snapfile()
	if err == nil {
		errs = append(errs, snapfile.Close())
	}

	return errors.Join(errs...)
}
