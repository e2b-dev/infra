package build

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func createLayerFromFolder(localFolder string) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := filepath.WalkDir(localFolder, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("error accessing path %q during walk: %w", path, walkErr)
		}

		// Skip the root directory itself, we only want its contents
		if path == localFolder {
			return nil
		}

		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("failed to get FileInfo for %q: %w", path, err)
		}

		relPath, err := filepath.Rel(localFolder, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path for %q against %q: %w", path, localFolder, err)
		}

		var header *tar.Header
		if fi.Mode()&os.ModeSymlink != 0 {
			linkTarget, errReadLink := os.Readlink(path)
			if errReadLink != nil {
				return fmt.Errorf("failed to read symlink target for %q: %w", path, errReadLink)
			}
			header, err = tar.FileInfoHeader(fi, linkTarget)
			if err != nil {
				return fmt.Errorf("failed to create tar header for symlink %q (target %q): %w", path, linkTarget, err)
			}
		} else {
			// For regular files and directories, the link argument to FileInfoHeader is ignored.
			header, err = tar.FileInfoHeader(fi, "")
			if err != nil {
				return fmt.Errorf("failed to create tar header for %q: %w", path, err)
			}
		}

		// Ensure the name in the tar header uses forward slashes and is the relative path.
		header.Name = filepath.ToSlash(relPath)

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header for %q (name in archive: %q): %w", path, header.Name, err)
		}

		// Only write content for regular files.
		// Directories and symlinks have their type encoded in the header and don't have content in the tar stream.
		if fi.Mode().IsRegular() {
			file, errOpen := os.Open(path)
			if errOpen != nil {
				return fmt.Errorf("failed to open file %q for archiving: %w", path, errOpen)
			}
			defer file.Close()

			if _, errCopy := io.Copy(tw, file); errCopy != nil {
				// This is where "archive/tar: write too long" would occur if header.Size was smaller than actual file content
				return fmt.Errorf("failed to copy content of file %q to tar archive (name in archive: %q): %w", path, header.Name, errCopy)
			}
		}
		return nil
	})

	if err != nil {
		// If WalkDir returned an error, close the tar writer and return the error.
		// tw.Close() might also return an error, but the WalkDir error is likely more pertinent.
		// We still attempt to close to flush any remaining data / write trailing zeros,
		// though it might also fail if the WalkDir error left the writer in a bad state.
		closeErr := tw.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("error during tar walk (%w) and also error during tar writer close (%w)", err, closeErr)
		}
		return nil, fmt.Errorf("error walking directory %q for tar creation: %w", localFolder, err)
	}

	// Close the tar writer to finalize the archive (e.g., write trailing zeros).
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("failed to close tar writer for %q: %w", localFolder, err)
	}

	// Create a new layer from the tarball stream.
	layer := stream.NewLayer(io.NopCloser(&buf))
	return layer, nil
}

type layerFile struct {
	Bytes []byte
	Mode  int64 // Permission and mode bits
}

// LayerFile creates a layer from a single file map. These layers are reproducible and consistent.
// A filemap is a path -> file content map representing a file system.
func LayerFile(filemap map[string]layerFile) (v1.Layer, error) {
	b := &bytes.Buffer{}
	w := tar.NewWriter(b)

	fn := []string{}
	for f := range filemap {
		fn = append(fn, f)
	}
	sort.Strings(fn)

	for _, f := range fn {
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
