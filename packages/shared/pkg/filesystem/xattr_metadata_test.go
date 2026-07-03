package filesystem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/xattr"
)

func TestWriteAndReadMetadata(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	want := map[string]string{"author": "mish", "purpose": "upload"}
	if err := writeMetadataForTest(path, want); err != nil {
		if IsXattrUnsupported(err) {
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

	if err := xattr.Set(path, MetadataXattrPrefix+"empty", []byte{}); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Set: %v", err)
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

// TestReadMetadataSkipsInvalidUTF8 verifies that metadata set directly inside
// the sandbox (e.g. via setfattr) with non-UTF-8 bytes is skipped on read, so
// it can't break proto marshaling of EntryInfo.metadata. Valid entries on the
// same file are still returned.
func TestReadMetadataSkipsInvalidUTF8(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := xattr.Set(path, MetadataXattrPrefix+"bad", []byte{0xff, 0xfe}); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Set: %v", err)
	}
	if err := xattr.Set(path, MetadataXattrPrefix+"good", []byte("ok")); err != nil {
		t.Fatalf("Set good: %v", err)
	}

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if _, leaked := got["bad"]; leaked {
		t.Errorf("non-UTF-8 value should be skipped, got %v", got)
	}
	if got["good"] != "ok" {
		t.Errorf("expected good=ok, got %v", got)
	}
}

func TestReadMetadataIgnoresNonUserPrefix(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := xattr.Set(path, MetadataXattrPrefix+"keep", []byte("yes")); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Set: %v", err)
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

	if err := xattr.Set(path, "user.foreign", []byte("ignored")); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("Set: %v", err)
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

	if err := writeMetadataForTest(path, map[string]string{"author": "mish", "purpose": "upload"}); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("WriteMetadata: %v", err)
	}

	// Foreign user.* xattr must survive every WriteMetadata call.
	if err := xattr.Set(path, "user.foreign", []byte("keep")); err != nil {
		t.Fatalf("Set foreign: %v", err)
	}

	// Non-empty call replaces the full set: "purpose" is dropped because
	// it's not in the new map.
	if err := writeMetadataForTest(path, map[string]string{"author": "alice"}); err != nil {
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
	if err := writeMetadataForTest(path, nil); err != nil {
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
	if _, err := xattr.Get(path, "user.foreign"); err != nil {
		t.Errorf("foreign xattr was removed: %v", err)
	}
}

func TestWriteMetadataSurvivesRename(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	renamed := filepath.Join(dir, "renamed")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer file.Close()

	if err := os.Rename(path, renamed); err != nil {
		t.Fatalf("rename file: %v", err)
	}

	want := map[string]string{"author": "mish"}
	if err := WriteMetadata(file, want); err != nil {
		if IsXattrUnsupported(err) {
			t.Skipf("filesystem does not support xattrs: %v", err)
		}
		t.Fatalf("WriteMetadata: %v", err)
	}

	got, err := ReadMetadata(renamed)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got["author"] != want["author"] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func writeMetadataForTest(path string, metadata map[string]string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	return WriteMetadata(file, metadata)
}
