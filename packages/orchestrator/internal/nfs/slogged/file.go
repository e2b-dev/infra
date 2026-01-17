package slogged

import "github.com/go-git/go-billy/v5"

type loggedFile struct {
	inner billy.File
}

var _ billy.File = (*loggedFile)(nil)

func wrapFile(f billy.File) billy.File {
	return &loggedFile{inner: f}
}

func (l loggedFile) Name() string {
	return l.inner.Name()
}

func (l loggedFile) Write(p []byte) (n int, err error) {
	slogStart("File.Write", len(p))
	defer func() { slogEndWithError("File.Write", err, n) }()

	return l.inner.Write(p)
}

func (l loggedFile) Read(p []byte) (n int, err error) {
	slogStart("File.Read", len(p))
	defer func() { slogEndWithError("File.Read", err, n) }()

	return l.inner.Read(p)
}

func (l loggedFile) ReadAt(p []byte, off int64) (n int, err error) {
	slogStart("File.ReadAt", len(p), off)
	defer func() { slogEndWithError("File.ReadAt", err, n) }()

	return l.inner.ReadAt(p, off)
}

func (l loggedFile) Seek(offset int64, whence int) (n int64, err error) {
	slogStart("File.Seek", offset, whence)
	defer func() { slogEndWithError("File.Seek", err, n) }()

	return l.inner.Seek(offset, whence)
}

func (l loggedFile) Close() (err error) {
	slogStart("File.Close")
	defer func() { slogEndWithError("File.Close", err) }()

	return l.inner.Close()
}

func (l loggedFile) Lock() (err error) {
	slogStart("File.Lock")
	defer func() { slogEndWithError("File.Lock", err) }()

	return l.inner.Lock()
}

func (l loggedFile) Unlock() (err error) {
	slogStart("File.Unlock")
	defer func() { slogEndWithError("File.Unlock", err) }()

	return l.inner.Unlock()
}

func (l loggedFile) Truncate(size int64) (err error) {
	slogStart("File.Truncate", size)
	defer func() { slogEndWithError("File.Truncate", err) }()

	return l.inner.Truncate(size)
}
