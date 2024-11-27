package template

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type storageTemplate struct {
	files *storage.TemplateCacheFiles

	memfile  *utils.SetOnce[block.ReadonlyDevice]
	rootfs   *utils.SetOnce[block.ReadonlyDevice]
	snapfile *utils.SetOnce[*storageFile]
}

func newTemplateFromStorage(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (*storageTemplate, error) {
	files, err := storage.NewTemplateFiles(
		templateId,
		buildId,
		kernelVersion,
		firecrackerVersion,
		hugePages,
	).NewTemplateCacheFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache files: %w", err)
	}

	return &storageTemplate{
		files:    files,
		memfile:  utils.NewSetOnce[block.ReadonlyDevice](),
		rootfs:   utils.NewSetOnce[block.ReadonlyDevice](),
		snapfile: utils.NewSetOnce[*storageFile](),
	}, nil
}

func (t *storageTemplate) Fetch(ctx context.Context, bucket *gcs.BucketHandle) {
	err := os.MkdirAll(t.files.CacheDir(), os.ModePerm)
	if err != nil {
		errMsg := fmt.Errorf("failed to create directory %s: %w", t.files.CacheDir(), err)

		t.memfile.SetError(errMsg)
		t.rootfs.SetError(errMsg)
		t.snapfile.SetError(errMsg)

		return
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() error {
		defer wg.Done()

		snapfile, snapfileErr := newStorageFile(
			ctx,
			bucket,
			t.files.StorageSnapfilePath(),
			t.files.CacheSnapfilePath(),
		)
		if snapfileErr != nil {
			errMsg := fmt.Errorf("failed to fetch snapfile: %w", snapfileErr)

			return t.snapfile.SetError(errMsg)
		}

		return t.snapfile.SetValue(snapfile)
	}()

	wg.Add(1)
	go func() error {
		defer wg.Done()

		memfileStorage, memfileErr := block.NewStorage(
			ctx,
			bucket,
			t.files.StorageMemfilePath(),
			t.files.MemfilePageSize(),
			t.files.CacheMemfilePath(),
		)
		if memfileErr != nil {
			errMsg := fmt.Errorf("failed to create memfile storage: %w", memfileErr)

			return t.memfile.SetError(errMsg)
		}

		return t.memfile.SetValue(memfileStorage)
	}()

	wg.Add(1)
	go func() error {
		defer wg.Done()

		rootfsStorage, rootfsErr := block.NewStorage(
			ctx,
			bucket,
			t.files.StorageRootfsPath(),
			t.files.RootfsBlockSize(),
			t.files.CacheRootfsPath(),
		)
		if rootfsErr != nil {
			errMsg := fmt.Errorf("failed to create rootfs storage: %w", rootfsErr)

			return t.rootfs.SetError(errMsg)
		}

		return t.rootfs.SetValue(rootfsStorage)
	}()

	wg.Wait()
}

func (t *storageTemplate) Close() error {
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

func (t *storageTemplate) Files() *storage.TemplateCacheFiles {
	return t.files
}

func (t *storageTemplate) Memfile() (block.ReadonlyDevice, error) {
	return t.memfile.Wait()
}

func (t *storageTemplate) Rootfs() (block.ReadonlyDevice, error) {
	return t.rootfs.Wait()
}

func (t *storageTemplate) Snapfile() (File, error) {
	return t.snapfile.Wait()
}
