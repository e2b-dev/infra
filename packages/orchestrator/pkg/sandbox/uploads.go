package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	errUploadInFlight  = errors.New("upload already in flight for build")
	ErrBuildNotInCache = errors.New("build not in template cache")
)

const futureTTL = 1 * time.Hour

type templateLookup interface {
	GetCachedTemplate(buildID string) (template.Template, bool)
}

// Uploads is the in-flight upload table. Each entry's future fires when its
// build's V4 header has been swapped, gating child layers that depend on it.
type Uploads struct {
	tc      templateLookup
	futures *ttlcache.Cache[uuid.UUID, *utils.ErrorOnce]
}

func NewUploads(tc *template.Cache) *Uploads {
	futures := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, *utils.ErrorOnce](futureTTL),
	)
	go futures.Start()

	return &Uploads{tc: tc, futures: futures}
}

func (p *Uploads) Stop() {
	p.futures.Stop()
}

// Start replaces a finished future at the same key; rejects an in-flight one.
func (p *Uploads) Start(buildID uuid.UUID) (*utils.ErrorOnce, error) {
	if existing := p.futures.Get(buildID); existing != nil {
		select {
		case <-existing.Value().Done():
		default:
			return nil, fmt.Errorf("%w: %s", errUploadInFlight, buildID)
		}
	}

	fut := utils.NewErrorOnce()
	p.futures.Set(buildID, fut, ttlcache.DefaultTTL)

	return fut, nil
}

// Wait returns the post-SwapHeader header. ErrBuildNotInCache is the P2P seam.
func (p *Uploads) Wait(ctx context.Context, buildID uuid.UUID, t build.DiffType) (*header.Header, error) {
	ctx, span := tracer.Start(ctx, "wait-for-parent-upload", trace.WithAttributes(
		attribute.String("build_id", buildID.String()),
		attribute.String("file_type", string(t)),
	))
	defer span.End()

	if item := p.futures.Get(buildID); item != nil {
		if err := item.Value().WaitWithContext(ctx); err != nil {
			return nil, fmt.Errorf("wait for upload %s: %w", buildID, err)
		}
	}

	d, err := p.find(ctx, buildID, t)
	if err != nil {
		return nil, err
	}

	h := d.Header()
	if h.IncompletePendingUpload {
		return nil, fmt.Errorf("build %s/%s: header incomplete", buildID, t)
	}

	return h, nil
}

func (p *Uploads) find(ctx context.Context, buildID uuid.UUID, t build.DiffType) (block.ReadonlyDevice, error) {
	tpl, ok := p.tc.GetCachedTemplate(buildID.String())
	if !ok {
		return nil, fmt.Errorf("build %s: %w", buildID, ErrBuildNotInCache)
	}

	switch t {
	case build.Memfile:
		return tpl.Memfile(ctx)
	case build.Rootfs:
		return tpl.Rootfs()
	default:
		return nil, fmt.Errorf("unsupported file type: %s", t)
	}
}
