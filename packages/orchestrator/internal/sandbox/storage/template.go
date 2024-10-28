package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg"
	nbd "github.com/e2b-dev/infra/packages/block-storage/pkg/nbd"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	templateDataExpiration = time.Hour * 72
	pageSize               = 2 << 11
	hugepageSize           = 2 << 20
	rootfsBlockSize        = 2 << 11
)

type Template struct {
	Files   *templateStorage.TemplateFiles
	nbdPool *nbd.DevicePool

	Memfile  func() (*blockStorage.BlockStorage, error)
	Rootfs   func() (*blockStorage.BlockStorage, error)
	Snapfile func() (*PrefetchedFile, error)

	rootfsResult   chan valueWithErr[*blockStorage.BlockStorage]
	memfileResult  chan valueWithErr[*blockStorage.BlockStorage]
	snapfileResult chan valueWithErr[*PrefetchedFile]

	hugePages bool
}

type valueWithErr[T any] struct {
	value T
	err   error
}

func (t *TemplateCache) newTemplate(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) *Template {
	rootfsResult := make(chan valueWithErr[*blockStorage.BlockStorage], 1)
	memfileResult := make(chan valueWithErr[*blockStorage.BlockStorage], 1)
	snapfileResult := make(chan valueWithErr[*PrefetchedFile], 1)

	h := &Template{
		rootfsResult:   rootfsResult,
		memfileResult:  memfileResult,
		snapfileResult: snapfileResult,
		hugePages:      hugePages,
		Files:          templateStorage.NewTemplateFiles(templateId, buildId, kernelVersion, firecrackerVersion),
		nbdPool:        t.nbdPool,
		Memfile: sync.OnceValues(func() (*blockStorage.BlockStorage, error) {
			result := <-memfileResult

			return result.value, result.err
		}),
		Rootfs: sync.OnceValues(func() (*blockStorage.BlockStorage, error) {
			result := <-rootfsResult

			return result.value, result.err
		}),
		Snapfile: sync.OnceValues(func() (*PrefetchedFile, error) {
			result := <-snapfileResult

			fmt.Printf(">>>> [][] snapfile: %s\n", result.value.Path)
			return result.value, result.err
		}),
	}

	return h
}

func (t *Template) Fetch(ctx context.Context, bucket *storage.BucketHandle) {
	err := os.MkdirAll(t.Files.CacheDir(), os.ModePerm)
	if err != nil {
		errMsg := fmt.Errorf("failed to create directory %s: %w", t.Files.CacheDir(), err)

		t.memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
			err: errMsg,
		}

		t.rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
			err: errMsg,
		}

		t.snapfileResult <- valueWithErr[*PrefetchedFile]{
			err: errMsg,
		}

		return
	}

	go func() {
		snapfile := newPrefetchedFile(ctx, bucket, t.Files.StorageSnapfilePath(), t.Files.CacheSnapfilePath())

		snapfileErr := snapfile.fetch()
		if snapfileErr != nil {
			t.snapfileResult <- valueWithErr[*PrefetchedFile]{
				err: fmt.Errorf("failed to fetch snapfile: %w", snapfileErr),
			}

			return
		}

		t.snapfileResult <- valueWithErr[*PrefetchedFile]{
			value: snapfile,
		}
	}()

	go func() {
		memfileObject := blockStorage.NewBucketObject(
			ctx,
			bucket,
			t.Files.StorageMemfilePath(),
		)

		var memfileBlockSize int64
		if t.hugePages {
			memfileBlockSize = hugepageSize
		} else {
			memfileBlockSize = pageSize
		}

		memfileStorage, memfileErr := blockStorage.New(
			ctx,
			memfileObject,
			t.Files.CacheMemfilePath(),
			memfileBlockSize,
		)
		if memfileErr != nil {
			t.memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
				err: fmt.Errorf("failed to create memfile storage: %w", memfileErr),
			}

			return
		}

		t.memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
			value: memfileStorage,
		}
	}()

	go func() {
		rootfsObject := blockStorage.NewBucketObject(
			ctx,
			bucket,
			t.Files.StorageRootfsPath(),
		)

		rootfsStorage, rootfsErr := blockStorage.New(
			ctx,
			rootfsObject,
			t.Files.CacheRootfsPath(),
			rootfsBlockSize,
		)
		if rootfsErr != nil {
			t.rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
				err: fmt.Errorf("failed to create rootfs storage: %w", rootfsErr),
			}

			return
		}

		t.rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
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
