package template

import (
	"context"
	"fmt"
	"os"

	template "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
)

const (
	pageSize        = 2 << 11
	hugepageSize    = 2 << 20
	rootfsBlockSize = 2 << 11
)

type Template struct {
	Files *template.TemplateCacheFiles

	Memfile *template.BlockStorage
	//Rootfs   *template.BlockStorage
	//Snapfile *File

	hugePages bool
}

func NewTemplate(
	ctx context.Context,
	bucket *storage.BucketHandle,
	cacheIdentifier,
	templateId,
	buildId,
	kernelVersion,
	firecrackerVersion string,
	hugePages bool,
) (*Template, error) {
	files := template.NewTemplateCacheFiles(
		template.NewTemplateFiles(
			templateId,
			buildId,
			kernelVersion,
			firecrackerVersion,
		),
		cacheIdentifier,
	)

	err := os.MkdirAll(files.CacheDir(), os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", files.CacheDir(), err)
	}

	var memfileBlockSize int64
	if hugePages {
		memfileBlockSize = hugepageSize
	} else {
		memfileBlockSize = pageSize
	}

	fmt.Printf("bucket: %v\n", template.BucketName)
	fmt.Printf("files.StorageMemfilePath(): %s\n", files.StorageMemfilePath())
	memfile := template.NewBlockStorage(
		ctx,
		bucket,
		files.StorageMemfilePath(),
		memfileBlockSize,
	)
	//
	//rootfs := template.NewBlockStorage(
	//	ctx,
	//	bucket,
	//	files.StorageRootfsPath(),
	//	rootfsBlockSize,
	//)
	//
	//snapfile, err := NewFile(ctx, bucket, files.StorageSnapfilePath(), files.CacheSnapfilePath())
	//if err != nil {
	//	return nil, fmt.Errorf("failed to fetch snapfile: %w", err)
	//}

	return &Template{
		Memfile: memfile,
		//Rootfs:    rootfs,
		//Snapfile:  snapfile,
		hugePages: hugePages,
		Files:     files,
	}, nil
}

func (t *Template) Close() error {
	memfileErr := t.Memfile.Close()

	//rootfsErr := t.Rootfs.Close()
	//
	//snapfileErr := t.Snapfile.Close()

	//return errors.Join(memfileErr, rootfsErr, snapfileErr)
	return memfileErr
}