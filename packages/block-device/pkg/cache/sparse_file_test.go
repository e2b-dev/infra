package cache

import (
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"
)

func TestSparseFileMarkerWithMmap(t *testing.T) {
	filePath := os.TempDir() + "/sparsefilemarker"

	tmpFile, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		t.Fatalf("Failed to create temp file: %s", err)
	}
	defer os.Remove(tmpFile.Name()) // clean up after the test
	defer tmpFile.Close()

	// Set up the file size and make it sparse
	fileSize := int64(10 * block.Size) // 10 blocks
	if err := fallocate(fileSize, tmpFile); err != nil {
		t.Fatalf("Failed to allocate space for temp file: %s", err)
	}

	// Create a SparseFileMarker instance
	sfm := NewSparseFileView(tmpFile)
	if sfm == nil {
		t.Fatal("NewSparseFileView() returned nil")
	}

	// Write data to create non-sparse areas at the beginning and end
	if _, err := tmpFile.WriteAt([]byte("start"), 0); err != nil {
		t.Fatalf("Failed to write to temp file: %s", err)
	}
	if _, err := tmpFile.WriteAt([]byte("end"), fileSize-3); err != nil {
		t.Fatalf("Failed to write to temp file: %s", err)
	}

	// Test MarkedBlockRange in the middle of the file
	start, end, err := sfm.MarkedBlockRange(4 * block.Size)
	if err != nil {
		t.Errorf("MarkedBlockRange failed: %s", err)
	}

	expectedStart := int64(5)
	expectedEnd := fileSize - 3
	if start != expectedStart || end != expectedEnd {
		t.Errorf("Expected marked range (%d, %d), got (%d, %d)", expectedStart, expectedEnd, start, end)
	}
}
