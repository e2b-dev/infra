package peerserver

import (
	"context"
	"fmt"
	"io"
	"os"

	tmpl "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// chunkSendSize is the maximum number of bytes per Send call.
const chunkSendSize = storage.MemoryChunkSize

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

// chunkWriter buffers writes and forwards them to a Sender in chunkSendSize-bounded calls.
type chunkWriter struct {
	sender Sender
	buf    []byte
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	total := 0

	for len(p) > 0 {
		space := chunkSendSize - len(w.buf)
		take := min(len(p), space)
		w.buf = append(w.buf, p[:take]...)
		p = p[take:]
		total += take

		if len(w.buf) == chunkSendSize {
			if err := w.flush(); err != nil {
				return total, err
			}
		}
	}

	return total, nil
}

func (w *chunkWriter) flush() error {
	if len(w.buf) == 0 {
		return nil
	}

	chunk := w.buf
	w.buf = nil

	return w.sender.Send(chunk)
}
