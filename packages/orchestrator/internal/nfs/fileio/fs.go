package fileio

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-billy/v5"
)

// LocalFS implements billy.Filesystem using direct filesystem I/O.
type LocalFS struct {
	rootPath string
}

func (f LocalFS) String() string {
	return fmt.Sprintf("LocalFS{rootPath=%s}", f.rootPath)
}

var _ billy.Filesystem = (*LocalFS)(nil)

// NewLocalFS creates a new LocalFS rooted at the given path.
func NewLocalFS(rootPath string) *LocalFS {
	return &LocalFS{rootPath: rootPath}
}

func (f LocalFS) resolvePath(filename string) string {
	// Clean the path to prevent directory traversal
	cleaned := filepath.Clean(filename)
	if filepath.IsAbs(cleaned) {
		// If it's already absolute, just return it (assuming it's within rootPath)
		return cleaned
	}

	return filepath.Join(f.rootPath, cleaned)
}

func (f LocalFS) Create(filename string) (billy.File, error) {
	return f.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

func (f LocalFS) Open(filename string) (billy.File, error) {
	return f.OpenFile(filename, os.O_RDONLY, 0)
}

func (f LocalFS) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	path := f.resolvePath(filename)

	file, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}

	return newLocalFile(file), nil
}

func (f LocalFS) Stat(filename string) (os.FileInfo, error) {
	path := f.resolvePath(filename)

	return os.Stat(path)
}

func (f LocalFS) Rename(oldPath, newPath string) error {
	oldResolved := f.resolvePath(oldPath)
	newResolved := f.resolvePath(newPath)

	return os.Rename(oldResolved, newResolved)
}

func (f LocalFS) Remove(filename string) error {
	path := f.resolvePath(filename)

	return os.Remove(path)
}

func (f LocalFS) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (f LocalFS) TempFile(dir, prefix string) (billy.File, error) {
	if dir == "" {
		dir = f.rootPath
	} else {
		dir = f.resolvePath(dir)
	}

	file, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return nil, err
	}

	return newLocalFile(file), nil
}

func (f LocalFS) ReadDir(path string) ([]os.FileInfo, error) {
	resolved := f.resolvePath(path)

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, err
	}

	infos := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}

	return infos, nil
}

func (f LocalFS) MkdirAll(filename string, perm os.FileMode) error {
	path := f.resolvePath(filename)

	return os.MkdirAll(path, perm)
}

func (f LocalFS) Lstat(filename string) (os.FileInfo, error) {
	path := f.resolvePath(filename)

	return os.Lstat(path)
}

func (f LocalFS) Symlink(target, link string) error {
	linkPath := f.resolvePath(link)

	return os.Symlink(target, linkPath)
}

func (f LocalFS) Readlink(link string) (string, error) {
	linkPath := f.resolvePath(link)

	return os.Readlink(linkPath)
}

func (f LocalFS) Chroot(path string) (billy.Filesystem, error) {
	resolved := f.resolvePath(path)

	return NewLocalFS(resolved), nil
}

func (f LocalFS) Root() string {
	return f.rootPath
}
