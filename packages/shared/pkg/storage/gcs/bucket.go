package gcs

import (
	"context"
	"sync"

	"cloud.google.com/go/storage"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type BucketHandle = storage.BucketHandle

var getClient = sync.OnceValue(func() *storage.Client {
	return utils.Must(newClient(context.Background()))
})

func newBucket(bucket string) *BucketHandle {
	return getClient().Bucket(bucket)
}

func getTemplateBucketName() string {
	return utils.RequiredEnv("TEMPLATE_BUCKET_NAME", "bucket for storing template files")
}

func GetTemplateBucket() *BucketHandle {
	return newBucket(getTemplateBucketName())
}
