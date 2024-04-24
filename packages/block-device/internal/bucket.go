package internal

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/e2b-dev/infra/packages/block-device/internal/backend"
)

type BucketSource struct {
	ctx    context.Context
	cancel context.CancelFunc

	source      *backend.GCS
	cache       *backend.MmapCache
	prefetecher *backend.Prefetcher
	chunker     *backend.ChunkerSyncer

	size int64
}

func NewServer(
	ctx context.Context,
	bucketName,
	bucketPath,
	bucketCachePath string,
	size int64,
) (*BucketSource, error) {
	cacheExists := false
	if _, err := os.Stat(bucketCachePath); err == nil {
		cacheExists = true
	}

	source, err := backend.NewGCS(
		ctx,
		bucketName,
		bucketPath,
	)
	if err != nil {
		return nil, err
	}

	cache, err := backend.NewMmapCache(size, bucketCachePath, cacheExists)
	if err != nil {
		return nil, err
	}

	chunker := backend.NewChunkerSyncer(source, cache)

	prefetcher := backend.NewPrefetcher(cache, size)
	go prefetcher.Start()

	return &BucketSource{
		source:      source,
		cache:       cache,
		prefetecher: prefetcher,
		chunker:     chunker,

		size: size,
	}, nil
}

func (d *BucketSource) ReadAt(p []byte, off int64) (n int, err error) {
	return d.chunker.ReadAt(p, off)
}

func (d *BucketSource) Close() error {
	d.cancel()

	d.prefetecher.Close()
	sourceErr := d.source.Close()
	cacheErr := d.cache.Close()

	return errors.Join(sourceErr, cacheErr)
}

func (d *BucketSource) CreateOverlay(cachePath string) (*BucketOverlay, error) {
	return newBucketOverlay(d.source, cachePath, d.size)
}

type BucketOverlay struct {
	overlay *backend.Overlay
	cache   *backend.MmapCache
}

func newBucketOverlay(source io.ReaderAt, cachePath string, size int64) (*BucketOverlay, error) {
	cacheExists := false
	if _, err := os.Stat(cachePath); err == nil {
		cacheExists = true
	}

	cache, err := backend.NewMmapCache(size, cachePath, cacheExists)
	if err != nil {
		return nil, err
	}

	overlay := backend.NewOverlay(source, cache, true)

	return &BucketOverlay{
		overlay: overlay,
		cache:   cache,
	}, nil
}

func (o *BucketOverlay) ReadAt(p []byte, off int64) (n int, err error) {
	return o.overlay.ReadAt(p, off)
}

func (o *BucketOverlay) Close() error {
	return o.cache.Close()
}
