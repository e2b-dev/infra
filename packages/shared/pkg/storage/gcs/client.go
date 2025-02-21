package gcs

import (
	"context"
	"fmt"

	"cloud.google.com/go/storage"
)

func newClient(ctx context.Context) (*storage.Client, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}

	return client, nil
}
