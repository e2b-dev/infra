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

// TestReadMetadataIgnoresForeignUserXattrs verifies that user.* xattrs
// written by other tooling (outside the user.e2b.* namespace) are not
// surfaced as envd metadata.
func TestReadMetadataIgnoresForeignUserXattrs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := unix.Setxattr(path, "user.foreign", []byte("ignored"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Setxattr: %v", err)
	}

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no envd metadata, got %v", got)
	}
}

// TestWriteMetadataReplacesFullSet verifies that WriteMetadata replaces the
// full set of user.e2b.* xattrs: a non-empty call drops stale keys and sets
// the new ones, and an empty/nil call clears every key. Xattrs outside the
// e2b namespace must survive in both cases.
func TestWriteMetadataReplacesFullSet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := WriteMetadata(path, map[string]string{"author": "mish", "purpose": "upload"}); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("WriteMetadata: %v", err)
	}

	// Foreign user.* xattr must survive every WriteMetadata call.
	if err := unix.Setxattr(path, "user.foreign", []byte("keep"), 0); err != nil {
		t.Fatalf("Setxattr foreign: %v", err)
	}

	// Non-empty call replaces the full set: "purpose" is dropped because
	// it's not in the new map.
	if err := WriteMetadata(path, map[string]string{"author": "alice"}); err != nil {
		t.Fatalf("WriteMetadata replace: %v", err)
	}
	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata after replace: %v", err)
	}
	if want := map[string]string{"author": "alice"}; len(got) != len(want) || got["author"] != want["author"] {
		t.Errorf("want %v, got %v", want, got)
	}

	// Empty/nil call clears all e2b metadata.
	if err := WriteMetadata(path, nil); err != nil {
		t.Fatalf("WriteMetadata nil: %v", err)
	}
	got, err = ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata after nil: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil write should clear all metadata, got %v", got)
	}

	// Foreign xattr untouched throughout.
	if _, err := unix.Getxattr(path, "user.foreign", nil); err != nil {
		t.Errorf("foreign xattr was removed: %v", err)
	}
}
