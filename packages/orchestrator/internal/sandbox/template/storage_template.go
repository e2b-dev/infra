package template

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type storageTemplate struct {
	files *storage.TemplateCacheFiles

	memfile  *utils.SetOnce[*Storage]
	rootfs   *utils.SetOnce[*Storage]
	snapfile *utils.SetOnce[File]

	memfileHeader *header.Header
	rootfsHeader  *header.Header
	localSnapfile *LocalFile

	persistence storage.StorageProvider
}

func newTemplateFromStorage(
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
	memfileHeader *header.Header,
	rootfsHeader *header.Header,
	persistence storage.StorageProvider,
	localSnapfile *LocalFile,
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
		files:         files,
		localSnapfile: localSnapfile,
		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
		persistence:   persistence,
		memfile:       utils.NewSetOnce[*Storage](),
		rootfs:        utils.NewSetOnce[*Storage](),
		snapfile:      utils.NewSetOnce[File](),
	}, nil
}

func (t *storageTemplate) Fetch(ctx context.Context, buildStore *build.DiffStore) {
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
		if t.localSnapfile != nil {
			return t.snapfile.SetValue(t.localSnapfile)
		}

		snapfile, snapfileErr := newStorageFile(
			ctx,
			t.persistence,
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

		memfileStorage, memfileErr := NewStorage(
			ctx,
			buildStore,
			t.files.BuildId,
			build.Memfile,
			t.files.MemfilePageSize(),
			t.memfileHeader,
			t.persistence,
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

		rootfsStorage, rootfsErr := NewStorage(
			ctx,
			buildStore,
			t.files.BuildId,
			build.Rootfs,
			t.files.RootfsBlockSize(),
			t.rootfsHeader,
			t.persistence,
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
	return closeTemplate(t)
}

func (t *storageTemplate) Files() *storage.TemplateCacheFiles {
	return t.files
}

func (t *storageTemplate) Memfile() (*Storage, error) {
	return t.memfile.Wait()
}

func (t *storageTemplate) Rootfs() (*Storage, error) {
	return t.rootfs.Wait()
}

func (t *storageTemplate) Snapfile() (File, error) {
	return t.snapfile.Wait()
}
