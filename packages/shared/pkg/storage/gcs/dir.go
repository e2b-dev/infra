package gcs

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func RemoveDir(ctx context.Context, bucket *BucketHandle, dir string) error {
	objects := bucket.Objects(ctx, &storage.Query{
		Prefix: dir + "/",
	})

	for {
		object, err := objects.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			return fmt.Errorf("error when iterating over template objects: %w", err)
		}

		err = bucket.Object(object.Name).Delete(ctx)
		if err != nil {
			return fmt.Errorf("error when deleting template object: %w", err)
		}
	}

	return nil
}
