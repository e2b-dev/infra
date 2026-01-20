package recovery

import "github.com/go-git/go-billy/v5"

type file struct {
	inner billy.File
}

var _ billy.File = (*file)(nil)

func wrapFile(f billy.File) billy.File {
	if f == nil {
		return nil
	}

	return &file{inner: f}
}

func (f *file) Name() string {
	defer tryRecovery("File.Name")

	return f.inner.Name()
}

func (f *file) Write(p []byte) (int, error) {
	defer tryRecovery("File.Write")

	return f.inner.Write(p)
}

func (f *file) Read(p []byte) (int, error) {
	defer tryRecovery("File.Read")

	return f.inner.Read(p)
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	defer tryRecovery("File.ReadAt")

	return f.inner.ReadAt(p, off)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	defer tryRecovery("File.Seek")

	return f.inner.Seek(offset, whence)
}

func (f *file) Close() error {
	defer tryRecovery("File.Close")

	return f.inner.Close()
}

func (f *file) Lock() error {
	defer tryRecovery("File.Lock")

	return f.inner.Lock()
}

func (f *file) Unlock() error {
	defer tryRecovery("File.Unlock")

	return f.inner.Unlock()
}

func (f *file) Truncate(size int64) error {
	defer tryRecovery("File.Truncate")

	return f.inner.Truncate(size)
}
