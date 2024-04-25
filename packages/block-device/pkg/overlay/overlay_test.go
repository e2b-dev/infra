package overlay

import (
	"bytes"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)


func TestOverlayWriteAt(t *testing.T) {
	base := &mockDevice{data: make(map[int64][]byte)}
	cache := &mockDevice{data: make(map[int64][]byte)}
	overlay := New(base, cache, true)

	data := []byte("hello world")
	n, err := overlay.WriteAt(data, 0)
	if err != nil {
		t.Fatalf("WriteAt failed: %s", err)
	}
	if n != len(data) {
		t.Errorf("Expected %d bytes written, got %d", len(data), n)
	}

	if _, ok := cache.data[0]; !ok {
		t.Errorf("Data was not written to cache")
	}
}

func TestOverlayReadAt(t *testing.T) {
	baseData := []byte("hello")
	cacheData := []byte("world")

	base := &mockDevice{data: map[int64][]byte{5: baseData}}
	cache := &mockDevice{data: map[int64][]byte{0: cacheData}}
	overlay := New(base, cache, true)

	// Test reading from cache
	buf := make([]byte, 5)
	_, err := overlay.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("ReadAt failed: %s", err)
	}
	if !bytes.Equal(buf, cacheData) {
		t.Errorf("Expected %s, got %s", cacheData, buf)
	}

	// Test reading from base and writing back to cache
	buf = make([]byte, 5)
	_, err = overlay.ReadAt(buf, 5)
	if err != nil {
		t.Fatalf("ReadAt failed: %s", err)
	}
	if !bytes.Equal(buf, baseData) {
		t.Errorf("Expected %s, got %s", baseData, buf)
	}
	if _, ok := cache.data[5]; !ok {
		t.Errorf("Data was not written back to cache")
	}
}
