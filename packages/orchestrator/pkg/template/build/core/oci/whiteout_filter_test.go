//go:build linux

package oci

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func filterTarEntries(t *testing.T, in *bytes.Buffer) map[string]string {
	t.Helper()

	tr := tar.NewReader(dropRedundantWhiteouts(in))
	entries := map[string]string{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		var body bytes.Buffer
		_, err = io.Copy(&body, tr)
		require.NoError(t, err)
		entries[hdr.Name] = body.String()
	}

	return entries
}

func writeReg(t *testing.T, tw *tar.Writer, name, content string) {
	t.Helper()

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(content)),
	}))
	_, err := io.WriteString(tw, content)
	require.NoError(t, err)
}

func TestDropRedundantWhiteouts(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// rm -rf-style delete: directory whiteout plus redundant nested entries.
	writeReg(t, tw, ".wh.foo", "")
	writeReg(t, tw, "foo/.wh.bar", "")
	writeReg(t, tw, "foo/nested/leftover", "gone")

	// Unrelated whiteout and file must pass through.
	writeReg(t, tw, "dir/.wh.removed", "")
	writeReg(t, tw, "dir/kept.txt", "kept")

	// Opaque marker is a meta whiteout, not a deletion of "..opq".
	writeReg(t, tw, "opaque/.wh..wh..opq", "")
	writeReg(t, tw, "opaque/file", "opaque-content")

	require.NoError(t, tw.Close())

	entries := filterTarEntries(t, &buf)

	assert.Equal(t, map[string]string{
		".wh.foo":             "",
		"dir/.wh.removed":     "",
		"dir/kept.txt":        "kept",
		"opaque/.wh..wh..opq": "",
		"opaque/file":         "opaque-content",
	}, entries)
}

func TestDropRedundantWhiteoutsPassthrough(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "dir/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}))
	writeReg(t, tw, "dir/file", "content")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "dir/link",
		Typeflag: tar.TypeSymlink,
		Linkname: "../elsewhere",
		Mode:     0o777,
	}))
	require.NoError(t, tw.Close())

	entries := filterTarEntries(t, &buf)

	assert.Equal(t, map[string]string{
		"dir/":     "",
		"dir/file": "content",
		"dir/link": "",
	}, entries)
}
