package main

import (
	"context"
	"fmt"
	"os"

	gcpstorage "cloud.google.com/go/storage"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func currentProcess() (header, error) {
	ctx := context.Background()

	objectKey := fmt.Sprintf("testing-%s", uuid.NewString())

	// upload file to gcp
	uploader, err := storage.NewMultipartUploaderWithRetryConfig(
		ctx, bucketName, objectKey,
		storage.DefaultRetryConfig())
	if err != nil {
		return header{}, fmt.Errorf("failed to create uploader: %w", err)
	}

	if err = uploader.UploadFileInParallel(ctx, bigfile, workerCount); err != nil {
		return header{}, fmt.Errorf("upload failed: %w", err)
	}

	stat, err := os.Stat(bigfile)
	if err != nil {
		return header{}, fmt.Errorf("stat failed: %w", err)
	}

	chunks := chunks(readChunkSize, stat.Size())
	var items []mapping
	for index, chunk := range chunks {
		items = append(items, mapping{
			index:        int64(index),
			offset:       chunk.offset,
			size:         chunk.length,
			remoteOffset: chunk.offset,
			remoteSize:   chunk.length,
		})
	}

	return header{
		path:  objectKey,
		items: items,
	}, nil
}

type uncompressed struct{}

func (u uncompressed) GetSlab(ctx context.Context, path string, item mapping) ([]byte, error) {
	client, err := gcpstorage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	bucket := client.Bucket(bucketName)
	handle := bucket.Object(path)
	reader, err := handle.NewRangeReader(ctx, item.remoteOffset, item.remoteSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create range reader: %w", err)
	}

	buffer := make([]byte, item.remoteSize)
	n := 0
	for reader.Remain() > 0 {
		count, err := reader.Read(buffer[n:])
		if err != nil {
			return nil, fmt.Errorf("failed to read: %w", err)
		}
		n += count
	}

	return buffer, nil
}

func getUncompressedClient() slabGetter {
	return &uncompressed{}
}
