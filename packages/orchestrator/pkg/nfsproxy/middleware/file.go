package middleware

import (
	"context"

	"github.com/go-git/go-billy/v5"
)

type wrappedFile struct {
	inner billy.File
	chain *Chain
	ctx   context.Context //nolint:containedctx
}

var _ billy.File = (*wrappedFile)(nil)

// WrapFile wraps a billy.File with the interceptor chain.
func WrapFile(ctx context.Context, f billy.File, chain *Chain) billy.File {
	if f == nil {
		return nil
	}

	return &wrappedFile{inner: f, chain: chain, ctx: ctx}
}

func (w *wrappedFile) Name() string {
	return w.inner.Name()
}

func (w *wrappedFile) Write(p []byte) (n int, err error) {
	err = w.chain.Exec(w.ctx, FileWriteRequest{Data: p},
		func(_ context.Context) error {
			n, err = w.inner.Write(p)

			return err
		})

	return n, err
}

func (w *wrappedFile) Read(p []byte) (n int, err error) {
	err = w.chain.Exec(w.ctx, FileReadRequest{Buffer: p},
		func(_ context.Context) error {
			n, err = w.inner.Read(p)

			return err
		})

	return n, err
}

func (w *wrappedFile) ReadAt(p []byte, off int64) (n int, err error) {
	err = w.chain.Exec(w.ctx, FileReadAtRequest{Buffer: p, Offset: off},
		func(_ context.Context) error {
			n, err = w.inner.ReadAt(p, off)

			return err
		})

	return n, err
}

func (w *wrappedFile) Seek(offset int64, whence int) (n int64, err error) {
	err = w.chain.Exec(w.ctx, FileSeekRequest{Offset: offset, Whence: whence},
		func(_ context.Context) error {
			n, err = w.inner.Seek(offset, whence)

			return err
		})

	return n, err
}

func (w *wrappedFile) Close() error {
	return w.chain.Exec(w.ctx, FileCloseRequest{},
		func(_ context.Context) error {
			return w.inner.Close()
		})
}

func (w *wrappedFile) Lock() error {
	return w.chain.Exec(w.ctx, FileLockRequest{},
		func(_ context.Context) error {
			return w.inner.Lock()
		})
}

func (w *wrappedFile) Unlock() error {
	return w.chain.Exec(w.ctx, FileUnlockRequest{},
		func(_ context.Context) error {
			return w.inner.Unlock()
		})
}

func (w *wrappedFile) Truncate(size int64) error {
	return w.chain.Exec(w.ctx, FileTruncateRequest{Size: size},
		func(_ context.Context) error {
			return w.inner.Truncate(size)
		})
}
