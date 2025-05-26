package rootfs

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type DirectProvider struct {
	tracer trace.Tracer

	cache *block.Cache
	path  string

	// TODO: Remove when the snapshot flow is improved
	finishedOperations chan struct{}
	// TODO: Remove when the snapshot flow is improved
	exporting atomic.Bool
}

func NewDirectProvider(tracer trace.Tracer, rootfs block.ReadonlyDevice, path string) (Provider, error) {
	size, err := rootfs.Size()
	if err != nil {
		return nil, fmt.Errorf("error getting device size: %w", err)
	}

	blockSize := rootfs.BlockSize()

	cache, err := block.NewCache(size, blockSize, path, true)
	if err != nil {
		return nil, fmt.Errorf("error creating cache: %w", err)
	}

	return &DirectProvider{
		tracer: tracer,
		cache:  cache,
		path:   path,

		finishedOperations: make(chan struct{}, 1),
	}, nil
}

func (o *DirectProvider) Start(_ context.Context) error {
	return nil
}

func (o *DirectProvider) ExportDiff(
	ctx context.Context,
	out io.Writer,
	stopSandbox func(context.Context) error,
) (*header.DiffMetadata, error) {
	ctx, childSpan := o.tracer.Start(ctx, "direct-provider-export")
	defer childSpan.End()

	o.exporting.CompareAndSwap(false, true)

	// the error is already logged in go routine in SandboxCreate handler
	go func() {
		err := stopSandbox(ctx)
		if err != nil {
			zap.L().Error("error stopping sandbox on cow export", zap.Error(err))
		}
	}()

	select {
	case <-o.finishedOperations:
		break
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for overlay device to be released")
	}
	telemetry.ReportEvent(ctx, "sandbox stopped")

	o.cache.MarkAllAsDirty()
	m, err := o.cache.ExportToDiff(out)
	if err != nil {
		return nil, fmt.Errorf("error exporting cache: %w", err)
	}

	telemetry.ReportEvent(ctx, "cache exported")

	err = o.cache.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing cache: %w", err)
	}

	return m, nil
}

func (o *DirectProvider) Close(_ context.Context) error {
	o.finishedOperations <- struct{}{}

	if !o.exporting.CompareAndSwap(false, true) {
		return nil
	}

	return o.cache.Close()
}

func (o *DirectProvider) Path() (string, error) {
	return o.path, nil
}
