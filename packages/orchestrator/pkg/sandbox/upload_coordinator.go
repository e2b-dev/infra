package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// uploadFutureTTL is how long a build's upload future is retained after
// registration. The future is only needed during the upload window (start →
// SwapHeader); 1h gives ample slack for the slowest plausible upload while
// keeping the map bounded.
const uploadFutureTTL = 1 * time.Hour

type templateCacheLookup interface {
	GetCachedTemplate(buildID string) (template.Template, bool)
}

// UploadCoordinator gates a child layer's V4 header finalization on its parent's
// SwapHeader via a per-build ErrorOnce. Failed uploads keep their future in the
// cache so late waiters observe the error; the cache TTL bounds the total.
type UploadCoordinator struct {
	templateCache templateCacheLookup // *template.Cache, but abstracted for testing

	futures *ttlcache.Cache[uuid.UUID, *utils.ErrorOnce]
}

func NewUploadCoordinator(templateCache *template.Cache) *UploadCoordinator {
	futures := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, *utils.ErrorOnce](uploadFutureTTL),
	)
	go futures.Start()

	return &UploadCoordinator{
		templateCache: templateCache,
		futures:       futures,
	}
}

// Stop tears down the TTL eviction goroutine. Safe to call multiple times.
func (c *UploadCoordinator) Stop() {
	c.futures.Stop()
}

var ErrUploadAlreadyInFlight = errors.New("upload already in flight for build")

func (c *UploadCoordinator) Begin(buildID uuid.UUID) (*utils.ErrorOnce, error) {
	if existing := c.futures.Get(buildID); existing != nil {
		select {
		case <-existing.Value().Done():
			// Prior upload finished; safe to replace.
		default:
			return nil, fmt.Errorf("%w: %s", ErrUploadAlreadyInFlight, buildID)
		}
	}

	fut := utils.NewErrorOnce()
	c.futures.Set(buildID, fut, ttlcache.DefaultTTL)

	return fut, nil
}

// WaitForFinalHeader blocks on buildID's upload future (if registered) and
// returns the post-SwapHeader file's header. ErrBuildNotInCache is the P2P
// seam — a future Redis-backed remote wait will plug in here.
func (c *UploadCoordinator) WaitForFinalHeader(ctx context.Context, buildID uuid.UUID, fileType build.DiffType) (*header.Header, error) {
	if item := c.futures.Get(buildID); item != nil {
		if err := item.Value().WaitWithContext(ctx); err != nil {
			return nil, fmt.Errorf("wait for upload %s: %w", buildID, err)
		}
	}

	tpl, ok := c.templateCache.GetCachedTemplate(buildID.String())
	if !ok {
		return nil, fmt.Errorf("build %s: %w", buildID, ErrBuildNotInCache)
	}

	dev, err := pickForFileType(ctx, tpl, fileType)
	if err != nil {
		return nil, fmt.Errorf("load %s for build %s: %w", fileType, buildID, err)
	}

	h := dev.Header()
	if h.IncompletePendingUpload {
		return nil, fmt.Errorf("build %s/%s: header incomplete", buildID, fileType)
	}

	return h, nil
}

// FindInTemplateCache returns the cached file device for buildID/fileType, or
// ErrBuildNotInCache if absent.
func (c *UploadCoordinator) FindInTemplateCache(ctx context.Context, buildID uuid.UUID, fileType build.DiffType) (block.ReadonlyDevice, error) {
	tpl, ok := c.templateCache.GetCachedTemplate(buildID.String())
	if !ok {
		return nil, fmt.Errorf("build %s: %w", buildID, ErrBuildNotInCache)
	}

	return pickForFileType(ctx, tpl, fileType)
}

var ErrBuildNotInCache = errors.New("build not in template cache")

func pickForFileType(ctx context.Context, tpl template.Template, fileType build.DiffType) (block.ReadonlyDevice, error) {
	switch fileType {
	case build.Memfile:
		return tpl.Memfile(ctx)
	case build.Rootfs:
		return tpl.Rootfs()
	default:
		return nil, fmt.Errorf("unsupported file type: %s", fileType)
	}
}
