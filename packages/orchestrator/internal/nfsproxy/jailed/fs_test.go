package jailed

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	billy "github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/stretchr/testify/assert"
)

// newJailedFS is a small helper to construct jailedFS with an in-memory FS.
func newJailedFS(prefix string, inner billy.Filesystem) jailedFS {
	if inner == nil {
		inner = memfs.New()
	}

	return jailedFS{prefix: prefix, inner: inner}
}

func TestJailedFS_Join_AlwaysPrefixed(t *testing.T) {
	t.Parallel()

	const prefix = "/jail"
	j := newJailedFS(prefix, memfs.New())

	// A variety of inputs, including attempts to traverse or absolute paths.
	cases := []struct {
		elems    []string
		expected string
	}{
		{[]string{"a"}, "/jail/a"},
		{[]string{"a", "b"}, "/jail/a/b"},
		{[]string{"./a", "./b"}, "/jail/a/b"},
		{[]string{"../a"}, "/jail/a"},
		{[]string{"../../a/b"}, "/jail/a/b"},
		{[]string{"/"}, "/jail"},
		{[]string{"/", "a"}, "/jail/a"},
		{[]string{"/jail/a"}, "/jail/a"},           // already prefixed
		{[]string{"/jail/../a"}, "/jail/a"},        // weird but should normalize and keep prefix
		{[]string{"..", "..", "etc"}, "/jail/etc"}, // multi-level traversal
		{[]string{"a", "..", "..", "..", "/etc/passwd"}, "/jail/etc/passwd"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("Join(%v)", tc.elems), func(t *testing.T) {
			t.Parallel()

			got := j.Join(tc.elems...)

			// All non-empty joins should produce a path that stays within the jail
			// and is properly prefixed.
			if len(tc.elems) == 0 {
				t.Fatalf("test case should not be empty")
			}

			// Normalize to slash for assertion parity.
			got = filepath.ToSlash(got)
			assert.Equal(t, tc.expected, got)
			assert.Truef(t, strings.HasPrefix(got+"/", prefix+"/"), "Join(%q) = %q; want path starting with %q", tc.elems, got, prefix+"/")
		})
	}
}

func TestJailedFS_Join_NoDoublePrefix(t *testing.T) {
	t.Parallel()

	const prefix = "/jail"
	j := newJailedFS(prefix, memfs.New())

	got := j.Join(prefix, "a")
	got = filepath.ToSlash(got)

	if !strings.HasPrefix(got, prefix+"/") {
		t.Fatalf("expected path to start with single prefix; got %q", got)
	}

	// Ensure we didn't add the prefix twice like /jail//jail/a
	if strings.Contains(got, prefix+"/"+prefix+"/") {
		t.Fatalf("path has duplicated prefix: %q", got)
	}
}
