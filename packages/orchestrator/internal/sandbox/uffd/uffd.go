package uffd

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/cache"
)

const (
	hugePageSize = 2 * 1024 * 1024 // 2 MB
)

var memfileCache = cache.NewMmapfileCache()

type Uffd struct {
	socketPath  string
	memfilePath string

	envID   string
	buildID string

	handler *Handler
}

func (u *Uffd) Start(ctx context.Context, tracer trace.Tracer) error {
	_, childSpan := tracer.Start(ctx, "start-uffd", trace.WithAttributes())
	defer childSpan.End()

	mf, err := memfileCache.GetMmapfile(u.memfilePath, fmt.Sprintf("%s-%s", u.envID, u.buildID))
	if err != nil {
		return fmt.Errorf("failed to get mmapfile: %w", err)
	}

	handler, err := NewHandler(hugePageSize)
	if err != nil {
		return fmt.Errorf("failed to create handler: %w", err)
	}

	err = handler.Start(u.socketPath, mf)
	if err != nil {
		return fmt.Errorf("failed to start handler: %w", err)
	}

	go func() {
		err = handler.Wait()
		if err != nil {
			fmt.Printf("uffd handler exited with error: %v\n", err)
		}
	}()

	return nil
}

func (u *Uffd) Stop(ctx context.Context, tracer trace.Tracer) error {
	if u.handler != nil {
		return u.handler.Stop()
	}

	return nil
}

func New(
	memfilePath,
	socketPath,
	envID,
	buildID string,
) *Uffd {
	return &Uffd{
		envID:       envID,
		buildID:     buildID,
		memfilePath: memfilePath,
		socketPath:  socketPath,
	}
}
