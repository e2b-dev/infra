package template

import (
	"context"
	"errors"
	"fmt"
	"io"

	blockStorage "github.com/e2b-dev/infra/packages/block-storage/pkg/source"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

type TemplateStorage struct {
	bucket *storage.BucketHandle
}

func NewTemplateStorage(ctx context.Context, client *storage.Client, bucket string) *TemplateStorage {
	b := client.Bucket(bucket)

	return &TemplateStorage{
		bucket: b,
	}
}

func (t *TemplateStorage) Remove(ctx context.Context, templateID string) error {
	objects := t.bucket.Objects(ctx, &storage.Query{
		Prefix: templateID + "/",
	})

	for {
		object, err := objects.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return fmt.Errorf("error when iterating over template objects: %w", err)
		}

		err = t.bucket.Object(object.Name).Delete(ctx)
		if err != nil {
			return fmt.Errorf("error when deleting template object: %w", err)
		}
	}

	return nil
}

func (t *TemplateStorage) NewTemplateBuild(templateFiles *templateStorage.TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		bucket: t.bucket,
		files:  templateFiles,
	}
}

type TemplateBuild struct {
	bucket *storage.BucketHandle
	files  *templateStorage.TemplateFiles
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	objects := t.bucket.Objects(ctx, &storage.Query{
		Prefix: t.files.StorageDir() + "/",
	})

	for {
		object, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return fmt.Errorf("error when iterating over template build objects: %w", err)
		}

		err = t.bucket.Object(object.Name).Delete(ctx)
		if err != nil {
			return fmt.Errorf("error when deleting template build object: %w", err)
		}
	}

	return nil
}

func (t *TemplateBuild) UploadMemfile(ctx context.Context, memfile io.Reader) error {
	object := blockStorage.NewGCSObjectFromBucket(ctx, t.bucket, t.files.StorageMemfilePath())

	_, err := object.ReadFrom(memfile)
	if err != nil {
		return fmt.Errorf("error when uploading memfile: %w", err)
	}

	return nil
}

func (t *TemplateBuild) UploadRootfs(ctx context.Context, rootfs io.Reader) error {
	object := blockStorage.NewGCSObjectFromBucket(ctx, t.bucket, t.files.StorageRootfsPath())

	_, err := object.ReadFrom(rootfs)
	if err != nil {
		return fmt.Errorf("error when uploading rootfs: %w", err)
	}

	return nil
}

func (t *TemplateBuild) UploadSnapfile(ctx context.Context, snapfile io.Reader) error {
	object := blockStorage.NewGCSObjectFromBucket(ctx, t.bucket, t.files.StorageSnapfilePath())

	_, err := object.ReadFrom(snapfile)
	if err != nil {
		return fmt.Errorf("error when uploading snapfile: %w", err)
	}

	return nil
}
