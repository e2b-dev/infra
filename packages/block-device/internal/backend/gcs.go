package backend

import (
	"context"

	"cloud.google.com/go/storage"
)

type GCS struct {
	client *storage.Client
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewGCS(ctx context.Context, bucket, filepath string) (*GCS, error) {
	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		return nil, err
	}

	obj := client.Bucket(bucket).Object(filepath)

	return &GCS{
		client: client,
		object: obj,
		ctx:    ctx,
	}, nil
}

func (g *GCS) ReadAt(b []byte, off int64) (int, error) {
	reader, err := g.object.NewRangeReader(g.ctx, off, int64(len(b)))
	if err != nil {
		return 0, err
	}

	defer reader.Close()

	return reader.Read(b)
}

func (g *GCS) Close() error {
	return g.client.Close()
}
