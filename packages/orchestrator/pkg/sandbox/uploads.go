package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	errUploadInFlight  = errors.New("upload already in flight for build")
	ErrBuildNotInCache = errors.New("build not in template cache")
)

const (
	futureTTL = 1 * time.Hour

	// refreshHeaderBudget bounds how long an upload Wait polls GCS for a
	// parent's V4 header. Crosses orchestrators: A may still be uploading on a
	// remote orch when B's runV4 calls Wait(A) here. Matches the per-upload
	// bound in server.uploadTimeout — anything longer means the parent's
	// upload is itself stuck and would have failed on its own.
	refreshHeaderBudget = 20 * time.Minute

	// uploadDoneChannelPrefix is the Redis pub/sub channel prefix for per-build
	// upload-finished signals. Empty payload = success; non-empty = upload error.
	uploadDoneChannelPrefix = "orchestrator.upload.done." // followed by buildID String
)

type templateLookup interface {
	GetCachedTemplate(buildID string) (template.Template, bool)
}

// Uploads is the in-flight upload table. Each entry's future fires when its
// build's V4 header has been swapped, gating child layers that depend on it.
//
// Cross-orch coordination uses Redis pub/sub on per-build channels: the
// uploader publishes on Finish, consumers subscribe inside Wait while polling
// GCS. The Redis client is optional — nil falls back to ticker-only polling.
type Uploads struct {
	tc          templateLookup
	persistence storage.StorageProvider
	redis       redis.UniversalClient

	futures *ttlcache.Cache[uuid.UUID, *utils.ErrorOnce]
}

func NewUploads(tc *template.Cache, persistence storage.StorageProvider, redisClient redis.UniversalClient) *Uploads {
	futures := ttlcache.New(
		ttlcache.WithTTL[uuid.UUID, *utils.ErrorOnce](futureTTL),
	)
	go futures.Start()

	return &Uploads{tc: tc, persistence: persistence, redis: redisClient, futures: futures}
}

func (u *Uploads) Stop() {
	u.futures.Stop()
}

// Start replaces a finished future at the same key; rejects an in-flight one.
// Build IDs are unique per upload so concurrent Starts for the same key are
// not expected — the in-flight check only guards against accidental misuse.
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

// Wait returns the parent's post-upload V4 header. Same-orch waits on the local
// future; cross-orch refreshes from GCS when the locally-cached header is
// stale, optionally accelerated by a per-call Redis subscription.
func (u *Uploads) Wait(ctx context.Context, buildID uuid.UUID, t build.DiffType) (*header.Header, error) {
	ctx, span := tracer.Start(ctx, "wait-for-parent-upload", trace.WithAttributes(
		telemetry.WithBuildID(buildID.String()),
		attribute.String("file_type", string(t)),
	))
	defer span.End()

	if item := u.futures.Get(buildID); item != nil {
		if err := item.Value().WaitWithContext(ctx); err != nil {
			return nil, fmt.Errorf("wait for upload %s: %w", buildID, err)
		}
	}

	d, err := u.find(ctx, buildID, t)
	if errors.Is(err, ErrBuildNotInCache) {
		// Ancestor never resumed locally (typical for grand-grandparents
		// reached via mappings). It's necessarily finalized — load directly
		// from GCS without an in-memory device or future to track.
		hdrPath := storage.Paths{BuildID: buildID.String()}.HeaderFile(string(t))

		return header.LoadHeader(ctx, u.persistence, hdrPath)
	}
	if err != nil {
		return nil, err
	}

	h := d.Header()
	if h.IncompletePendingUpload {
		// The only way we can still have an incomplete header at this point is
		// the P2P path. We already waited on the local upload future and it did
		// not finalize the header.
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		h, err = build.PollRemoteStorageForHeader(ctx, u.persistence, buildID, t, u.subscribe(ctx, buildID), refreshHeaderBudget)
		if err != nil {
			return nil, err
		}
		d.SwapHeader(h)
	}

	return h, nil
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

// --- Cross-orch upload-done signaling (Redis pub/sub on per-build channels) ---

func uploadDoneChannel(buildID uuid.UUID) string {
	return uploadDoneChannelPrefix + buildID.String()
}

// publishUploadDoneToRedis broadcasts an upload-finished signal so cross-orch waiters can stop
// polling. Best-effort; failures fall through to the ticker poll. Empty
// payload = success; non-empty = the upload error message.
func (u *Uploads) publishUploadDoneToRedis(ctx context.Context, buildID uuid.UUID, uploadErr error) {
	if u.redis == nil {
		return
	}

	payload := ""
	if uploadErr != nil {
		payload = uploadErr.Error()
	}

	if err := u.redis.Publish(ctx, uploadDoneChannel(buildID), payload).Err(); err != nil {
		logger.L().Warn(ctx, "failed to publish upload-done signal",
			logger.WithBuildID(buildID.String()),
			zap.Error(err),
		)
	}
}

// subscribe opens a per-call SUBSCRIBE on buildID's upload-done channel and
// returns a channel that fires once with the upload outcome. The subscription
// is torn down when ctx cancels (caller must use a derived context). Returns
// a nil channel when Redis is not configured — nil channels never fire, so
// LoadV4 cleanly degrades to ticker-only polling.
func (u *Uploads) subscribe(ctx context.Context, buildID uuid.UUID) <-chan error {
	if u.redis == nil {
		return nil
	}

	out := make(chan error, 1)

	go func() {
		ps := u.redis.Subscribe(ctx, uploadDoneChannel(buildID))
		defer ps.Close()

		msg, err := ps.ReceiveMessage(ctx)
		if err != nil {
			return // ctx cancelled or connection error: silent (ticker covers)
		}

		var uploadErr error
		if msg.Payload != "" {
			uploadErr = errors.New(msg.Payload)
		}

		select {
		case out <- uploadErr:
		case <-ctx.Done():
		}
	}()

	return out
}
