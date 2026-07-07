//go:build linux

package oci

import (
	"archive/tar"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/moby/go-archive"
)

// dropRedundantWhiteouts filters a tar layer stream, dropping entries that
// are shadowed by an earlier whiteout of an ancestor directory.
//
// Some builders (e.g. kaniko) emit layers for `rm -rf /foo`-style deletes
// that contain both a whiteout for the directory (.wh.foo) and redundant
// nested entries below it (foo/.wh.bar). When unpacking with the overlay
// whiteout format, .wh.foo is converted to a 0:0 character device at foo, so
// any subsequent mknod/create below foo fails with ENOTDIR.
//
// The caller must Close the returned reader
func dropRedundantWhiteouts(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)
		err := filterRedundantWhiteouts(tar.NewReader(r), tw)
		if err == nil {
			err = tw.Close()
		}
		pw.CloseWithError(err)
	}()

	return pr
}

func filterRedundantWhiteouts(tr *tar.Reader, tw *tar.Writer) error {
	// Paths whited-out by earlier entries in this layer.
	deleted := make(map[string]struct{})

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		name := path.Clean(strings.TrimLeft(hdr.Name, "/"))

		if hasDeletedAncestor(name, deleted) {
			// Shadowed by an ancestor whiteout: skip entry (and its body).
			continue
		}

		if base := path.Base(name); strings.HasPrefix(base, archive.WhiteoutPrefix) &&
			// Meta markers (.wh..wh..opq opaque dirs, .wh..wh..plnk aufs
			// hardlink dirs) are not whiteouts of real paths.
			!strings.HasPrefix(base, archive.WhiteoutMetaPrefix) {
			deleted[path.Join(path.Dir(name), strings.TrimPrefix(base, archive.WhiteoutPrefix))] = struct{}{}
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return err
		}
	}
}

func hasDeletedAncestor(name string, deleted map[string]struct{}) bool {
	for p := path.Dir(name); p != "." && p != "/"; p = path.Dir(p) {
		if _, ok := deleted[p]; ok {
			return true
		}
	}

	return false
}
