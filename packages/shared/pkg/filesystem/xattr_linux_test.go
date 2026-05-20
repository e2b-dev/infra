//go:build linux

package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteAndReadMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	want := map[string]string{"author": "mish", "purpose": "upload"}
	if err := WriteMetadata(path, want); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("WriteMetadata: %v", err)
	}

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}

func TestReadMetadataEmptyValue(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := unix.Setxattr(path, MetadataXattrPrefix+"empty", []byte{}, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Setxattr: %v", err)
	}

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	v, ok := got["empty"]
	if !ok {
		t.Fatalf("expected key %q, got %v", "empty", got)
	}
	if v != "" {
		t.Errorf("expected empty string, got %q", v)
	}
}

func TestReadMetadataIgnoresNonUserPrefix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := unix.Setxattr(path, MetadataXattrPrefix+"keep", []byte("yes"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Setxattr: %v", err)
	}

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if _, ok := got["keep"]; !ok {
		t.Errorf("expected key %q in %v", "keep", got)
	}
	if _, leaked := got[MetadataXattrPrefix+"keep"]; leaked {
		t.Errorf("prefix %q leaked into keys: %v", MetadataXattrPrefix, got)
	}
}
