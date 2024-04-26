package overlay

import (
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
)

func TestOverlay(t *testing.T) {
	size := int64(30)
	offset := int64(10)
	base := block.NewMockDevice(make([]byte, size))
	cache := block.NewMockDevice(make([]byte, size))
	overlay := New(base, cache, true)

	data := []byte("hello world")
	n, err := overlay.WriteAt(data, offset)
	assert.NoError(t, err, "WriteAt failed")
	assert.Equal(t, len(data), n, "Expected %d bytes written, got %d", len(data), n)

	// Check if data was written to cache
	cacheData := make([]byte, len(data))
	_, err = cache.ReadAt(cacheData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.Equal(t, data, cacheData, "Data was not written to cache")

	// Check if base is not affected
	baseData := make([]byte, len(data))
	_, err = base.ReadAt(baseData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.NotEqual(t, data, baseData, "Base should not be affected by overlay write")

	// Check if reading from the overlay returns the correct data
	overlayData := make([]byte, len(data))
	_, err = overlay.ReadAt(overlayData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.Equal(t, data, overlayData, "Reading from overlay did not return the expected data")
}

func TestOverlayWithCacheReadsFalse(t *testing.T) {
	size := int64(30)
	offset := int64(10)
	base := block.NewMockDevice(make([]byte, size))
	cache := block.NewMockDevice(make([]byte, size))
	overlay := New(base, cache, false)

	data := []byte("hello world")
	n, err := overlay.WriteAt(data, offset)
	assert.NoError(t, err, "WriteAt failed")
	assert.Equal(t, len(data), n, "Expected %d bytes written, got %d", len(data), n)

	// Check if data was written to cache
	cacheData := make([]byte, len(data))
	_, err = cache.ReadAt(cacheData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.Equal(t, data, cacheData, "Data was not written to cache")

	// Check if base is not affected
	baseData := make([]byte, len(data))
	_, err = base.ReadAt(baseData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.NotEqual(t, data, baseData, "Base should not be affected by overlay write")

	// Check if reading from the overlay returns the correct data
	overlayData := make([]byte, len(data))
	_, err = overlay.ReadAt(overlayData, offset)
	assert.NoError(t, err, "ReadAt failed")
	assert.Equal(t, data, overlayData, "Reading from overlay did not return the expected data")
}
