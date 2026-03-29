package peerclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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

	attrOpWriteTo  = attribute.String("operation", "WriteTo")
	attrOpExists   = attribute.String("operation", "Exists")
	attrOpSize     = attribute.String("operation", "Size")
	attrOpGetFrame = attribute.String("operation", "GetFrame")

	attrResolveRedisError = attribute.String("peer_resolve", "redis_error")
	attrResolveNoPeer     = attribute.String("peer_resolve", "no_peer")
	attrResolveSelf       = attribute.String("peer_resolve", "self")
	attrResolveDialError  = attribute.String("peer_resolve", "dial_error")
	attrResolvePeer       = attribute.String("peer_resolve", "peer")
	attrResolveUploaded   = attribute.String("peer_resolve", "uploaded")

	attrPeerHitTrue  = attribute.Bool("peer_hit", true)
	attrPeerHitFalse = attribute.Bool("peer_hit", false)
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

	return newPeerStorageProvider(p.base, res.client, res.uploaded)
}

func (p *routingProvider) OpenBlob(ctx context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, _ := storage.ParseStoragePath(path)

	return p.resolveProvider(ctx, buildID).OpenBlob(ctx, path, objType)
}

func (p *routingProvider) OpenFramedFile(ctx context.Context, path string) (storage.FramedFile, error) {
	buildID, _ := storage.ParseStoragePath(path)

	return p.resolveProvider(ctx, buildID).OpenFramedFile(ctx, path)
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
	// uploaded is set when the peer signals GCS upload is complete (use_storage=true).
	// Once non-nil, all subsequent reads skip the peer and go to base.
	uploaded *atomic.Pointer[UploadedHeaders]
}

func newPeerStorageProvider(
	base storage.StorageProvider,
	peerClient orchestrator.ChunkServiceClient,
	uploaded *atomic.Pointer[UploadedHeaders],
) storage.StorageProvider {
	return &peerStorageProvider{
		base:       base,
		peerClient: peerClient,
		uploaded:   uploaded,
	}
}

func (p *peerStorageProvider) OpenBlob(_ context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, fileName := storage.ParseStoragePath(path)

	return &peerBlob{peerHandle: peerHandle[storage.Blob]{
		client:   p.peerClient,
		buildID:  buildID,
		fileName: fileName,
		uploaded: p.uploaded,
		openFn: func(ctx context.Context) (storage.Blob, error) {
			return p.base.OpenBlob(ctx, path, objType)
		},
	}}, nil
}

func (p *peerStorageProvider) OpenFramedFile(_ context.Context, path string) (storage.FramedFile, error) {
	buildID, fileName := storage.ParseStoragePath(path)

	return &peerFramedFile{peerHandle: peerHandle[storage.FramedFile]{
		client:   p.peerClient,
		buildID:  buildID,
		fileName: fileName,
		uploaded: p.uploaded,
		openFn: func(ctx context.Context) (storage.FramedFile, error) {
			return p.base.OpenFramedFile(ctx, path)
		},
	}}, nil
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

// checkPeerAvailability marks the build as uploaded when UseStorage is set.
func checkPeerAvailability(avail *orchestrator.PeerAvailability, uploaded *atomic.Pointer[UploadedHeaders]) bool {
	if avail.GetNotAvailable() {
		return false
	}

	if avail.GetUseStorage() {
		hdrs := &UploadedHeaders{
			MemfileHeader: avail.GetMemfileHeader(),
			RootfsHeader:  avail.GetRootfsHeader(),
		}
		uploaded.Store(hdrs)

		return false
	}

	return true
}

type peerHandle[Base any] struct {
	client   orchestrator.ChunkServiceClient
	buildID  string
	fileName string
	uploaded *atomic.Pointer[UploadedHeaders]

	mu     sync.Mutex
	base   Base
	loaded bool
	openFn func(ctx context.Context) (Base, error)
}

func (h *peerHandle[Base]) getOrOpenBase(ctx context.Context) (Base, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.loaded {
		return h.base, nil
	}

	b, err := h.openFn(ctx)
	if err != nil {
		var zero Base

		return zero, err
	}

	h.base = b
	h.loaded = true

	return b, nil
}

// peerAttempt is the result of a peer read attempt, used with withPeerFallback.
// hit=true means the peer had data (value is populated); when hit=true and the
// caller also returns a non-nil error the helper records a partial failure.
type peerAttempt[T any] struct {
	value T
	bytes int64
	hit   bool
}

func withPeerFallback[Base, T any](
	ctx context.Context,
	h *peerHandle[Base],
	spanName string,
	opAttr attribute.KeyValue,
	peerFn func(ctx context.Context) (peerAttempt[T], error),
	useBase func(ctx context.Context, base Base) (T, error),
) (T, error) {
	ctx, span := tracer.Start(ctx, spanName, trace.WithAttributes(
		attribute.String("file_name", h.fileName),
	))
	defer span.End()

	if h.uploaded.Load() == nil {
		timer := peerReadTimerFactory.Begin(opAttr)

		res, err := peerFn(ctx)
		if res.hit {
			if err != nil {
				span.RecordError(err)
				timer.Failure(ctx, res.bytes)

				return res.value, err
			}

			span.SetAttributes(attrPeerHitTrue)
			timer.Success(ctx, res.bytes)

			return res.value, nil
		}

		if err != nil {
			span.RecordError(err)
		}

		timer.Failure(ctx, 0)
	}

	span.SetAttributes(attrPeerHitFalse)

	base, err := h.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		var zero T

		return zero, err
	}

	result, err := useBase(ctx, base)
	if err != nil {
		span.RecordError(err)
	}

	return result, err
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
