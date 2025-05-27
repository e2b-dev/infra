package build

import (
	"archive/tar"
	"bytes"
	"io"
	"sort"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

type layerFile struct {
	Bytes []byte
	Mode  int64 // Permission and mode bits
}

// LayerFile creates a layer from a single file map. These layers are reproducible and consistent.
// A filemap is a path -> file content map representing a file system.
func LayerFile(filemap map[string]layerFile) (v1.Layer, error) {
	b := &bytes.Buffer{}
	w := tar.NewWriter(b)

	names := []string{}
	for f := range filemap {
		names = append(names, f)
	}
	sort.Strings(names)

	for _, f := range names {
		c := filemap[f]
		if err := w.WriteHeader(&tar.Header{
			Name: f,
			Size: int64(len(c.Bytes)),
			Mode: c.Mode,
		}); err != nil {
			return nil, err
		}
		if _, err := w.Write(c.Bytes); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}

	// Return a new copy of the buffer each time it's opened.
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBuffer(b.Bytes())), nil
	})
}

// LayerSymlink creates a layer from a single symlink map. These layers are reproducible and consistent.
func LayerSymlink(symlinks map[string]string) (v1.Layer, error) {
	b := &bytes.Buffer{}
	w := tar.NewWriter(b)

	names := make([]string, 0, len(symlinks))
	for name := range symlinks {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		target := symlinks[name]
		if err := w.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o777,
			Typeflag: tar.TypeSymlink,
			Linkname: target,
		}); err != nil {
			return nil, err
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewBuffer(b.Bytes())), nil
	})
}
