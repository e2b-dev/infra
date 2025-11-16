package rootfs

import (
	"context"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Provider interface {
	Start(ctx context.Context) error
	Close(ctx context.Context) error
	Path() (string, error)
	ExportDiff(ctx context.Context, out io.Writer, closeSandbox func(context.Context) error) (*header.DiffMetadata, error)
}
