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

func (w *wrappedFile) Write(p []byte) (int, error) {
	results, err := w.chain.Exec(w.ctx, "File.Write", []any{p},
		func(_ context.Context) ([]any, error) {
			n, err := w.inner.Write(p)

			return []any{n}, err
		})
	if err != nil {
		return 0, err
	}

	return results[0].(int), nil
}

func (w *wrappedFile) Read(p []byte) (int, error) {
	results, err := w.chain.Exec(w.ctx, "File.Read", []any{p},
		func(_ context.Context) ([]any, error) {
			n, err := w.inner.Read(p)

			return []any{n}, err
		})
	if err != nil {
		return 0, err
	}

	return results[0].(int), nil
}

func (w *wrappedFile) ReadAt(p []byte, off int64) (int, error) {
	results, err := w.chain.Exec(w.ctx, "File.ReadAt", []any{p, off},
		func(_ context.Context) ([]any, error) {
			n, err := w.inner.ReadAt(p, off)

			return []any{n}, err
		})
	if err != nil {
		return 0, err
	}

	return results[0].(int), nil
}

func (w *wrappedFile) Seek(offset int64, whence int) (int64, error) {
	results, err := w.chain.Exec(w.ctx, "File.Seek", []any{offset, whence},
		func(_ context.Context) ([]any, error) {
			n, err := w.inner.Seek(offset, whence)

			return []any{n}, err
		})
	if err != nil {
		return 0, err
	}

	return results[0].(int64), nil
}

func (w *wrappedFile) Close() error {
	_, err := w.chain.Exec(w.ctx, "File.Close", nil,
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Close()
		})

	return err
}

func (w *wrappedFile) Lock() error {
	_, err := w.chain.Exec(w.ctx, "File.Lock", nil,
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Lock()
		})

	return err
}

func (w *wrappedFile) Unlock() error {
	_, err := w.chain.Exec(w.ctx, "File.Unlock", nil,
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Unlock()
		})

	return err
}

func (w *wrappedFile) Truncate(size int64) error {
	_, err := w.chain.Exec(w.ctx, "File.Truncate", []any{size},
		func(_ context.Context) ([]any, error) {
			return nil, w.inner.Truncate(size)
		})

	return err
}
