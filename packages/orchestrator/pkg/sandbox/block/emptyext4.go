package block

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// NewEmptyExt4Cache creates a Cache pre-populated with an empty ext4 filesystem.
// All blocks are marked dirty so reads go through the cache (not the base device).
// Used as the overlay upper region in a CompositeDevice.
func NewEmptyExt4Cache(ctx context.Context, sizeMB int64, blockSize int64, cachePath string) (*Cache, error) {
	sizeBytes := sizeMB * 1024 * 1024

	// Create a temporary file, format it as ext4, then import into the cache
	tmpFile, err := os.CreateTemp("", "overlay-ext4-*.img")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := tmpFile.Truncate(sizeBytes); err != nil {
		tmpFile.Close()

		return nil, fmt.Errorf("truncate: %w", err)
	}
	tmpFile.Close()

	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-q", "-F", "-b", fmt.Sprintf("%d", blockSize), tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mkfs.ext4: %w (%s)", err, string(out))
	}

	// Create the cache and copy the ext4 image into it block by block
	cache, err := NewCache(sizeBytes, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("create cache: %w", err)
	}

	img, err := os.Open(tmpPath)
	if err != nil {
		cache.Close()

		return nil, fmt.Errorf("open ext4 image: %w", err)
	}
	defer img.Close()

	buf := make([]byte, blockSize)
	for off := int64(0); off < sizeBytes; off += blockSize {
		n, err := img.ReadAt(buf, off)
		if err != nil && off+int64(n) < sizeBytes {
			cache.Close()

			return nil, fmt.Errorf("read ext4 image at %d: %w", off, err)
		}

		if _, err := cache.WriteAt(buf[:n], off); err != nil {
			cache.Close()

			return nil, fmt.Errorf("write cache at %d: %w", off, err)
		}
	}

	return cache, nil
}

// emptyBaseDevice returns zeros for all reads. Used as the base layer
// for the overlay upper in a CompositeDevice — the actual data comes
// from the cache (which contains the ext4 filesystem).
type emptyBaseDevice struct {
	size      int64
	blockSize int64
}

func NewEmptyBaseDevice(size, blockSize int64) *emptyBaseDevice {
	return &emptyBaseDevice{size: size, blockSize: blockSize}
}

func (e *emptyBaseDevice) ReadAt(_ context.Context, p []byte, _ int64) (int, error) {
	clear(p)

	return len(p), nil
}

func (e *emptyBaseDevice) Slice(_ context.Context, _, _ int64) ([]byte, error) {
	return nil, BytesNotAvailableError{}
}

func (e *emptyBaseDevice) Size(_ context.Context) (int64, error) { return e.size, nil }
func (e *emptyBaseDevice) BlockSize() int64                      { return e.blockSize }
func (e *emptyBaseDevice) Header() *header.Header                { return nil }
func (e *emptyBaseDevice) Close() error                          { return nil }
