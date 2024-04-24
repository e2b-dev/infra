package block

import (
	"os"
	"testing"
)

func TestSparseFileMarkerWithMmap(t *testing.T) {
	// Create a temporary file to use for testing
	tmpFile, err := os.CreateTemp("", "sparsefilemarker")
	if err != nil {
		t.Fatalf("Failed to create temp file: %s", err)
	}
	defer os.Remove(tmpFile.Name()) // clean up after the test
	defer tmpFile.Close()

	// Set up the file size and make it sparse
	fileSize := int64(8192) // example size
	if err := tmpFile.Truncate(fileSize); err != nil {
		t.Fatalf("Failed to truncate temp file: %s", err)
	}

	// Create a SparseFileMarker instance
	sfm := NewSparseFileMarker(tmpFile)
	if sfm == nil {
		t.Fatal("NewSparseFileMarker() returned nil")
	}

	// Write data to create a non-sparse area at the beginning and end
	if _, err := tmpFile.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("Failed to write to temp file: %s", err)
	}
	if _, err := tmpFile.WriteAt([]byte("world"), fileSize-5); err != nil {
		t.Fatalf("Failed to write to temp file: %s", err)
	}

	// Test FirstUnmarked in the middle of the file
	start, err := sfm.FirstUnmarked(5)
	if err != nil {
		t.Errorf("FirstUnmarked failed: %s", err)
	}
	if start <= 5 || start >= fileSize-5 {
		t.Errorf("Expected start of hole between 5 and %d, got %d", fileSize-5, start)
	}
}
