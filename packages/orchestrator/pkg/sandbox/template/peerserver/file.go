//go:build linux

package peerserver

import (
	"context"
	"fmt"
	"io"
	"os"

	tmpl "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
)

var _ BlobSource = (*fileSource)(nil)

// fileSource serves local files.
// Supports Exists checks and full-content streaming.
type fileSource struct {
	getFile func() (tmpl.File, error)
}

func (f *fileSource) Exists(_ context.Context) (bool, error) {
	file, err := f.getFile()
	if err != nil {
		return false, err
	}

	if _, err := os.Stat(file.Path()); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (f *fileSource) Stream(ctx context.Context, sender Sender) error {
	_, span := tracer.Start(ctx, "stream-local-file")
	defer span.End()

	file, err := f.getFile()
	if err != nil {
		span.RecordError(err)

		return fmt.Errorf("get file: %w", err)
	}

	osFile, err := os.Open(file.Path())
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotAvailable
		}

		span.RecordError(err)

		return fmt.Errorf("open file %q: %w", file.Path(), err)
	}
	defer osFile.Close()

	w := &chunkWriter{sender: sender}
	if _, err := io.Copy(w, osFile); err != nil {
		span.RecordError(err)

		return fmt.Errorf("stream file: %w", err)
	}

	return w.flush()
}
