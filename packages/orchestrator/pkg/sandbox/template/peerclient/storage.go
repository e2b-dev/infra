package peerclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient")
	meter  = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient")

	peerReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.peer.read",
		"Duration of peer orchestrator reads",
		"Total bytes read from peer orchestrator",
		"Total peer orchestrator reads",
	))

	attrOpWriteTo     = attribute.String("operation", "WriteTo")
	attrOpExists      = attribute.String("operation", "Exists")
	attrOpSize        = attribute.String("operation", "Size")
	attrOpRangeReader = attribute.String("operation", "OpenRangeReader")

	attrResolveRedisError = attribute.String("peer_resolve", "redis_error")
	attrResolveNoPeer     = attribute.String("peer_resolve", "no_peer")
	attrResolveSelf       = attribute.String("peer_resolve", "self")
	attrResolveDialError  = attribute.String("peer_resolve", "dial_error")
	attrResolvePeer       = attribute.String("peer_resolve", "peer")
	attrResolveUploaded   = attribute.String("peer_resolve", "uploaded")

	attrPeerHitTrue       = attribute.String("peer_hit", "true")
	attrPeerHitFalse      = attribute.String("peer_hit", "false")
	attrPeerHitTransition = attribute.String("peer_hit", "transitioned")
	attrPeerHitMiss       = attribute.String("peer_hit", "miss")
)

var _ storage.StorageProvider = (*routingProvider)(nil)

// routingProvider wraps a base StorageProvider and, for each Open call,
// checks Redis for a peer routing entry for the buildID extracted from the path.
// This allows each layer in a multi-layer template to be independently routed to
// the peer that holds it, rather than routing all layers to a single peer.
type routingProvider struct {
	base     storage.StorageProvider
	resolver Resolver
}

func NewRoutingProvider(base storage.StorageProvider, resolver Resolver) storage.StorageProvider {
	return &routingProvider{base: base, resolver: resolver}
}

func (p *routingProvider) resolveProvider(ctx context.Context, buildID string) storage.StorageProvider {
	ctx, span := tracer.Start(ctx, "resolve peer-provider", trace.WithAttributes(
		telemetry.WithBuildID(buildID),
	))
	defer span.End()

	status, res := p.resolver.resolve(ctx, buildID)
	span.SetAttributes(status)

	if status != attrResolvePeer {
		return p.base
	}

	span.SetAttributes(attribute.String("peer_address", res.addr))

	return newPeerStorageProvider(p.base, res.client, res.state)
}

// PendingHeader implements storage.HeaderProvider.
func (p *routingProvider) PendingHeader(buildID, name string) *header.Header {
	return p.resolver.PendingHeader(buildID, name)
}

func (p *routingProvider) OpenBlob(ctx context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, _ := storage.SplitPath(path)

	return p.resolveProvider(ctx, buildID).OpenBlob(ctx, path, objType)
}

func (p *routingProvider) OpenSeekable(ctx context.Context, path string, objType storage.SeekableObjectType) (storage.Seekable, error) {
	buildID, _ := storage.SplitPath(path)

	return p.resolveProvider(ctx, buildID).OpenSeekable(ctx, path, objType)
}

func (p *routingProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	return p.base.DeleteObjectsWithPrefix(ctx, prefix)
}

func (p *routingProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return p.base.UploadSignedURL(ctx, path, ttl)
}

func (p *routingProvider) GetDetails() string {
	return p.base.GetDetails()
}

var _ storage.StorageProvider = (*peerStorageProvider)(nil)

// peerStorageProvider tries the peer first for reads. Writes are always delegated to base.
type peerStorageProvider struct {
	base       storage.StorageProvider
	peerClient orchestrator.ChunkServiceClient
	// state holds the per-buildID coordination shared by all peer{Blob,Seekable}
	// for this build: uploaded flag (route to base post-flip) and parsed V4
	// headers (delivered inline on UseStorage; build.File installs them).
	state *peerState
}

func newPeerStorageProvider(
	base storage.StorageProvider,
	peerClient orchestrator.ChunkServiceClient,
	state *peerState,
) storage.StorageProvider {
	return &peerStorageProvider{
		base:       base,
		peerClient: peerClient,
		state:      state,
	}
}

func (p *peerStorageProvider) OpenBlob(_ context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, t := storage.SplitPath(path)

	return &peerBlob{
		peerHandle: peerHandle{
			client:  p.peerClient,
			buildID: buildID,
			name:    t,
			state:   p.state,
		},
		openBase: func(ctx context.Context) (storage.Blob, error) {
			return p.base.OpenBlob(ctx, path, objType)
		},
	}, nil
}

func (p *peerStorageProvider) OpenSeekable(_ context.Context, path string, objType storage.SeekableObjectType) (storage.Seekable, error) {
	// Strip any compression suffix so peerSeekable holds the basic name. The
	// base fallthrough path composes the actual storage path from
	// (buildID, name, ct) per-call. Peer routing usually engages only
	// pre-finalization (basic name in, no-op strip), but the Redis peer-key
	// TTL outlives the upload by ~2 min: a fresh orchestrator can resolve a
	// stale entry for a finalized V4/Zstd build, in which case StorageDiff
	// hands us "buildID/memfile.zstd" — without stripping, getBase would
	// double-suffix to "memfile.zstd.zstd" on fallthrough.
	buildID, t := storage.SplitPath(path)
	t = storage.StripCompression(t)

	return &peerSeekable{
		peerHandle: peerHandle{
			client:  p.peerClient,
			buildID: buildID,
			name:    t,
			state:   p.state,
		},
		basePersistence: p.base,
		objType:         objType,
	}, nil
}

func (p *peerStorageProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	return p.base.DeleteObjectsWithPrefix(ctx, prefix)
}

func (p *peerStorageProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return p.base.UploadSignedURL(ctx, path, ttl)
}

func (p *peerStorageProvider) GetDetails() string {
	return p.base.GetDetails()
}

// checkPeerAvailability classifies a peer availability message. On UseStorage
// it flips state.uploaded and (if header bytes are present) stashes the
// parsed V4 header for File to install. Parse failures are logged and the
// transition still proceeds — the File keeps its existing header rather
// than installing a corrupted one, matching the V3 fallback path.
func checkPeerAvailability(ctx context.Context, avail *orchestrator.PeerAvailability, state *peerState, name string) result {
	if avail.GetUseStorage() {
		if state != nil {
			if bytes := avail.GetHeaderBytes(); len(bytes) > 0 {
				if h, err := header.DeserializeBytes(bytes); err == nil {
					state.setHeader(name, h)
				} else {
					logger.L().Error(ctx, "peer header deserialize failed", zap.String("file_name", name), zap.Error(err))
				}
			}
			state.uploaded.Store(true)
		}

		return transitioned
	}

	if avail.GetNotAvailable() {
		return missed
	}

	return served
}

// peerHandle holds the peer-side identity shared by peerBlob and peerSeekable.
type peerHandle struct {
	client  orchestrator.ChunkServiceClient
	buildID string
	name    string
	state   *peerState
}

// result enumerates how a peer attempt resolved. Zero value is unnamed and
// covers the early-return / real-error paths; tryPeer's default arm handles
// it as a failure.
type result int

const (
	served       result = iota + 1 // peer returned data
	missed                         // NotAvailable signal
	transitioned                   // UseStorage signal
)

type peerAttempt[T any] struct {
	value  T
	bytes  int64
	result result
}

// tryPeer runs peerFn if state.uploaded is still false, records telemetry,
// and returns (value, served, err). Availability signals (Miss/Transitioned)
// are recorded as Success and surface as served=false; only real RPC errors
// propagate as err.
func tryPeer[T any](
	ctx context.Context,
	h *peerHandle,
	spanName string,
	opAttr attribute.KeyValue,
	peerFn func(ctx context.Context) (peerAttempt[T], error),
) (T, bool, error) {
	ctx, span := tracer.Start(ctx, spanName, trace.WithAttributes(
		attribute.String("file_name", h.name),
	))
	defer span.End()

	var zero T
	if h.state != nil && h.state.uploaded.Load() {
		span.SetAttributes(attrPeerHitFalse)

		return zero, false, nil
	}

	timer := peerReadTimerFactory.Begin(opAttr)

	res, err := peerFn(ctx)
	switch res.result {
	case served:
		if err != nil {
			// partial failure: data was returned but streaming/closing failed
			span.RecordError(err)
			timer.Failure(ctx, res.bytes)

			return res.value, true, err
		}
		span.SetAttributes(attrPeerHitTrue)
		timer.Success(ctx, res.bytes)

		return res.value, true, nil

	case transitioned:
		span.SetAttributes(attrPeerHitTransition)
		timer.Success(ctx, 0)

	case missed:
		span.SetAttributes(attrPeerHitMiss)
		timer.Success(ctx, 0)

	default:
		if err != nil {
			span.RecordError(err)
		}
		span.SetAttributes(attrPeerHitFalse)
		timer.Failure(ctx, 0)
	}

	return zero, false, nil
}

var _ io.ReadCloser = (*peerStreamReader)(nil)

// peerStreamReader wraps a gRPC streaming recv function as an io.ReadCloser.
// cancel is called on Close to signal the server to terminate the stream.
type peerStreamReader struct {
	recv    func() ([]byte, error)
	current *bytes.Reader
	done    bool
	cancel  context.CancelFunc
}

func newPeerStreamReader(recv func() ([]byte, error), cancel context.CancelFunc) *peerStreamReader {
	return &peerStreamReader{
		recv:   recv,
		cancel: cancel,
	}
}

func (r *peerStreamReader) Read(p []byte) (int, error) {
	for {
		if r.current != nil && r.current.Len() > 0 {
			return r.current.Read(p)
		}

		if r.done {
			return 0, io.EOF
		}

		// gRPC Recv returns (nil, io.EOF) separately from the last data message,
		// so no data is lost here.
		data, err := r.recv()
		if errors.Is(err, io.EOF) {
			r.done = true

			return 0, io.EOF
		}
		if err != nil {
			return 0, fmt.Errorf("failed to receive chunk from peer: %w", err)
		}

		r.current = bytes.NewReader(data)
	}
}

func (r *peerStreamReader) Close() error {
	r.cancel()

	return nil
}
