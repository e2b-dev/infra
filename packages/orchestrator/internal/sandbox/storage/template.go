package storage

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

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
		Files:   templateStorage.NewTemplateFiles(templateId, buildId, kernelVersion, firecrackerVersion),
		nbdPool: t.nbdPool,
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

			return result.value, result.err
		}),
	}

	go func() {
		err := os.MkdirAll(h.Files.CacheDir(), os.ModePerm)
		if err != nil {
			errMsg := fmt.Errorf("failed to create directory %s: %w", h.Files.CacheDir(), err)

			memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
				err: errMsg,
			}

			rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
				err: errMsg,
			}

			snapfileResult <- valueWithErr[*PrefetchedFile]{
				err: errMsg,
			}

			return
		}

		go func() {
			snapfile := newPrefetchedFile(t.ctx, t.bucket, h.Files.StorageSnapfilePath(), h.Files.CacheSnapfilePath())

			err := snapfile.fetch()
			if err != nil {
				snapfileResult <- valueWithErr[*PrefetchedFile]{
					err: fmt.Errorf("failed to fetch snapfile: %w", err),
				}

				return
			}

			snapfileResult <- valueWithErr[*PrefetchedFile]{
				value: snapfile,
			}
		}()

		go func() {
			memfileObject := blockStorage.NewBucketObject(
				t.ctx,
				t.bucket,
				h.Files.StorageMemfilePath(),
			)

			var memfileBlockSize int64
			if hugePages {
				memfileBlockSize = hugepageSize
			} else {
				memfileBlockSize = pageSize
			}

			memfileStorage, err := blockStorage.New(
				t.ctx,
				memfileObject,
				h.Files.CacheMemfilePath(),
				memfileBlockSize,
			)
			if err != nil {
				memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
					err: fmt.Errorf("failed to create memfile storage: %w", err),
				}

				return
			}

			memfileResult <- valueWithErr[*blockStorage.BlockStorage]{
				value: memfileStorage,
			}
		}()

		go func() {
			rootfsObject := blockStorage.NewBucketObject(
				t.ctx,
				t.bucket,
				h.Files.StorageRootfsPath(),
			)

			rootfsStorage, err := blockStorage.New(
				t.ctx,
				rootfsObject,
				h.Files.CacheRootfsPath(),
				rootfsBlockSize,
			)
			if err != nil {
				rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
					err: fmt.Errorf("failed to create rootfs storage: %w", err),
				}

				return
			}

			rootfsResult <- valueWithErr[*blockStorage.BlockStorage]{
				value: rootfsStorage,
			}
		}()
	}()

	return h
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
