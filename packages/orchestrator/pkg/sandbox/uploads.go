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
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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
	tc          templateLookup
	persistence storage.StorageProvider
	futures     *ttlcache.Cache[uuid.UUID, *utils.ErrorOnce]
}

func NewUploads(tc *template.Cache, persistence storage.StorageProvider) *Uploads {
	futures := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, *utils.ErrorOnce](futureTTL),
	)
	go futures.Start()

	return &Uploads{tc: tc, persistence: persistence, futures: futures}
}

func (u *Uploads) Stop() {
	u.futures.Stop()
}

// Start replaces a finished future at the same key; rejects an in-flight one.
func (u *Uploads) Start(buildID uuid.UUID) (*utils.ErrorOnce, error) {
	if existing := u.futures.Get(buildID); existing != nil {
		select {
		case <-existing.Value().Done():
		default:
			return nil, fmt.Errorf("%w: %s", errUploadInFlight, buildID)
		}
	}

	fut := utils.NewErrorOnce()
	u.futures.Set(buildID, fut, ttlcache.DefaultTTL)

	return fut, nil
}

// refreshHeaderBudget bounds how long an upload Wait will poll GCS for a
// parent's V4 header to appear. Crosses orchestrators: A may still be
// uploading on a remote orch when B's runV4 calls Wait(A) here. The Redis
// pubsub follow-up will accelerate this via hint(); ticker fallback keeps
// it correct when a hint is missed.
const refreshHeaderBudget = 30 * time.Second

// Wait returns the parent's post-upload V4 header. Same-orch waits on the
// local future; cross-orch refreshes from GCS when the locally-cached header
// is stale. ErrBuildNotInCache is the P2P seam.
//
// Staleness signal: any header without a self-entry in Builds. A finalized
// V4 header always has Builds[buildID]; the in-flight diff header peer-served
// while the parent is still uploading does not.
func (u *Uploads) Wait(ctx context.Context, buildID uuid.UUID, t build.DiffType) (*header.Header, error) {
	ctx, span := tracer.Start(ctx, "wait-for-parent-upload", trace.WithAttributes(
		attribute.String("build_id", buildID.String()),
		attribute.String("file_type", string(t)),
	))
	defer span.End()

	if item := u.futures.Get(buildID); item != nil {
		if err := item.Value().WaitWithContext(ctx); err != nil {
			return nil, fmt.Errorf("wait for upload %s: %w", buildID, err)
		}
	}

	d, err := u.find(ctx, buildID, t)
	if err != nil {
		return nil, err
	}

	h := d.Header()
	if isStale(h, buildID) {
		fresh, err := u.refreshFromGCS(ctx, buildID, t)
		if err != nil {
			return nil, err
		}
		if fresh != nil {
			d.SwapHeader(fresh)
			h = fresh
		}
	}

	if h.IncompletePendingUpload {
		return nil, fmt.Errorf("build %s/%s: header incomplete", buildID, t)
	}

	return h, nil
}

// isStale reports whether h needs to be refreshed from GCS to be usable as a
// parent in V4 lineage construction. V3 builds (Version<V4) are skipped:
// they have no Builds map and nothing to upgrade to.
func isStale(h *header.Header, buildID uuid.UUID) bool {
	if h.IncompletePendingUpload {
		return true
	}
	if h.Metadata.Version < header.MetadataVersionV4 {
		return false
	}
	_, hasSelf := h.Builds[buildID]

	return !hasSelf
}

// refreshFromGCS polls storage for the post-upload V4 header. Returns nil if
// storage only has a V3 header (genuine V3 build, no upgrade available);
// returns the V4 header on success; returns the budget-expired error if the
// parent is still uploading on a remote orch and never lands.
func (u *Uploads) refreshFromGCS(ctx context.Context, buildID uuid.UUID, t build.DiffType) (*header.Header, error) {
	if u.persistence == nil {
		return nil, nil
	}

	h, err := build.LoadV4(ctx, u.persistence, buildID, t, u.hint(buildID), refreshHeaderBudget)
	if err != nil {
		return nil, err
	}
	if h.Metadata.Version < header.MetadataVersionV4 {
		return nil, nil
	}

	return h, nil
}

// hint returns a channel that closes/receives when a cross-orch upload-finished
// signal arrives for buildID. Currently always nil (ticker-only polling);
// Redis-pubsub wiring lands in plan_redis_signaling.md.
func (u *Uploads) hint(_ uuid.UUID) <-chan struct{} {
	return nil
}

func (u *Uploads) find(ctx context.Context, buildID uuid.UUID, t build.DiffType) (block.ReadonlyDevice, error) {
	tpl, ok := u.tc.GetCachedTemplate(buildID.String())
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
