package testutils

import (
	"context"
	"fmt"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type LoggerOverlay struct {
	overlay *block.Overlay
}

func NewLoggerOverlay(overlay *block.Overlay) *LoggerOverlay {
	return &LoggerOverlay{overlay: overlay}
}

func (l *LoggerOverlay) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stdout, "[read panic recovered]: [%d, %d] -> %v\n", off, len(p), r)
		}
	}()

	fmt.Fprintf(os.Stdout, "[read started]: [%d, %d]\n", off, len(p))

	n, err := l.overlay.ReadAt(ctx, p, off)

	fmt.Fprintf(os.Stdout, "[read completed]: [%d, %d] -> %d\n", off, len(p), n)

	return n, err
}

func (l *LoggerOverlay) WriteAt(p []byte, off int64) (int, error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stdout, "[write panic recovered]: [%d, %d] -> %v\n", off, len(p), r)
		}
	}()

	fmt.Fprintf(os.Stdout, "[write started]: [%d, %d]\n", off, len(p))

	n, err := l.overlay.WriteAt(p, off)

	fmt.Fprintf(os.Stdout, "[write completed]: [%d, %d] -> %d\n", off, len(p), n)

	return n, err
}

func (l *LoggerOverlay) Size(ctx context.Context) (int64, error) {
	return l.overlay.Size(ctx)
}

func (l *LoggerOverlay) BlockSize() int64 {
	return l.overlay.BlockSize()
}

func (l *LoggerOverlay) Header() *header.Header {
	return l.overlay.Header()
}

func (l *LoggerOverlay) Close() error {
	return l.overlay.Close()
}

func (l *LoggerOverlay) EjectCache() (*block.Cache, error) {
	return l.overlay.EjectCache()
}

func (l *LoggerOverlay) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return l.overlay.Slice(ctx, off, length)
}
