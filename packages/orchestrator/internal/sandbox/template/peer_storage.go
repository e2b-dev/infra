package template

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	peerOperationAttr        = "operation"
	peerOperationWriteTo     = "WriteTo"
	peerOperationExists      = "Exists"
	peerOperationSize        = "Size"
	peerOperationReadAt      = "ReadAt"
	peerOperationRangeReader = "OpenRangeReader"
)

var (
	_ storage.StorageProvider = (*peerStorageProvider)(nil)
	_ storage.Blob            = (*peerBlob)(nil)
	_ storage.Seekable        = (*peerSeekable)(nil)
	_ io.ReadCloser           = (*peerStreamReader)(nil)

	peerReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.peer.read",
		"Duration of peer orchestrator reads",
		"Total bytes read from peer orchestrator",
		"Total peer orchestrator reads",
	))
)

// peerResolver looks up peer addresses for build IDs via Redis and manages
// gRPC connections to peer orchestrators. It is used by peerRoutingStorageProvider
// to decide, per storage path, whether to read from a peer or from the base provider.
type peerResolver struct {
	redis       redis.UniversalClient // may be nil if Redis is disabled
	nodeAddress string                // "ip:port" of this node, used to skip self
	peerConns   sync.Map              // address → *grpc.ClientConn
}

func newPeerResolver(redisClient redis.UniversalClient, nodeAddress string) *peerResolver {
	return &peerResolver{
		redis:       redisClient,
		nodeAddress: nodeAddress,
	}
}

// readPeerAddress checks Redis for a peer routing entry for the given build.
// Returns ("", nil) when no entry exists (files are already in GCS or Redis is disabled).
func (r *peerResolver) readPeerAddress(ctx context.Context, buildID string) (string, error) {
	if r.redis == nil {
		return "", nil
	}

	addr, err := r.redis.Get(ctx, "peer:"+buildID).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}

	return addr, err
}

// getOrDialPeer returns a cached gRPC connection to the given address, dialing if needed.
func (r *peerResolver) getOrDialPeer(address string) (*grpc.ClientConn, error) {
	if conn, ok := r.peerConns.Load(address); ok {
		return conn.(*grpc.ClientConn), nil
	}

	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial peer %s: %w", address, err)
	}

	actual, loaded := r.peerConns.LoadOrStore(address, conn)
	if loaded {
		// Another goroutine stored a connection first — close ours and use theirs.
		_ = conn.Close()

		return actual.(*grpc.ClientConn), nil
	}

	return conn, nil
}

func (r *peerResolver) selfAddress() string {
	return r.nodeAddress
}

func (r *peerResolver) close() {
	r.peerConns.Range(func(_, value any) bool {
		_ = value.(*grpc.ClientConn).Close()

		return true
	})
}

var _ storage.StorageProvider = (*peerRoutingStorageProvider)(nil)

// peerRoutingStorageProvider wraps a base StorageProvider and, for each Open call,
// checks Redis for a peer routing entry for the buildID extracted from the path.
// This allows each layer in a multi-layer template to be independently routed to
// the peer that holds it, rather than routing all layers to a single peer.
type peerRoutingStorageProvider struct {
	base     storage.StorageProvider
	resolver *peerResolver
}

func newPeerRoutingStorageProvider(base storage.StorageProvider, resolver *peerResolver) storage.StorageProvider {
	return &peerRoutingStorageProvider{base: base, resolver: resolver}
}

// resolveProvider checks Redis for a peer address for the given buildID.
// Returns a peer-wrapped provider if a remote peer is found, otherwise returns the base.
func (p *peerRoutingStorageProvider) resolveProvider(ctx context.Context, buildID string) storage.StorageProvider {
	ctx, span := tracer.Start(ctx, "resolve-peer-provider", trace.WithAttributes(
		attribute.String("build_id", buildID),
	))
	defer span.End()

	addr, err := p.resolver.readPeerAddress(ctx, buildID)
	if err != nil {
		span.SetAttributes(attribute.String("peer_resolve", "redis_error"))
		span.RecordError(err)

		return p.base
	}

	if addr == "" {
		span.SetAttributes(attribute.String("peer_resolve", "no_peer"))

		return p.base
	}

	if addr == p.resolver.selfAddress() {
		span.SetAttributes(attribute.String("peer_resolve", "self"))

		return p.base
	}

	conn, dialErr := p.resolver.getOrDialPeer(addr)
	if dialErr != nil {
		span.SetAttributes(attribute.String("peer_resolve", "dial_error"))
		span.RecordError(dialErr)

		return p.base
	}

	span.SetAttributes(
		attribute.String("peer_resolve", "peer"),
		attribute.String("peer_address", addr),
	)

	return newPeerStorageProvider(p.base, orchestrator.NewChunkServiceClient(conn))
}

func (p *peerRoutingStorageProvider) OpenBlob(ctx context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, _, _ := strings.Cut(path, "/")

	return p.resolveProvider(ctx, buildID).OpenBlob(ctx, path, objType)
}

func (p *peerRoutingStorageProvider) OpenSeekable(ctx context.Context, path string, objType storage.SeekableObjectType) (storage.Seekable, error) {
	buildID, _, _ := strings.Cut(path, "/")

	return p.resolveProvider(ctx, buildID).OpenSeekable(ctx, path, objType)
}

func (p *peerRoutingStorageProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	return p.base.DeleteObjectsWithPrefix(ctx, prefix)
}

func (p *peerRoutingStorageProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return p.base.UploadSignedURL(ctx, path, ttl)
}

func (p *peerRoutingStorageProvider) GetDetails() string {
	return p.base.GetDetails()
}

// peerStorageProvider wraps a base StorageProvider and tries the peer orchestrator first
// for every read, falling back to the base provider when the peer is unavailable or
// does not have the requested file. Writes are always delegated to base.
// The build ID is derived from the storage path (format: "{buildID}/{fileName}"),
// so parent-build diffs are also routed to the peer with their correct build ID.
type peerStorageProvider struct {
	base       storage.StorageProvider
	peerClient orchestrator.ChunkServiceClient
	// uploaded is set to true when the peer signals that GCS upload is complete
	// (use_storage=true). Once set, all subsequent reads skip the peer and go to base.
	uploaded *atomic.Bool
}

func newPeerStorageProvider(
	base storage.StorageProvider,
	peerClient orchestrator.ChunkServiceClient,
) storage.StorageProvider {
	return &peerStorageProvider{
		base:       base,
		peerClient: peerClient,
		uploaded:   &atomic.Bool{},
	}
}

func (p *peerStorageProvider) OpenBlob(_ context.Context, path string, objType storage.ObjectType) (storage.Blob, error) {
	buildID, fileName, _ := strings.Cut(path, "/")

	return &peerBlob{
		client:      p.peerClient,
		base:        p.base,
		basePath:    path,
		baseObjType: objType,
		buildID:     buildID,
		fileName:    fileName,
		uploaded:    p.uploaded,
	}, nil
}

func (p *peerStorageProvider) OpenSeekable(_ context.Context, path string, objType storage.SeekableObjectType) (storage.Seekable, error) {
	buildID, fileName, _ := strings.Cut(path, "/")

	return &peerSeekable{
		client:      p.peerClient,
		base:        p.base,
		basePath:    path,
		baseObjType: objType,
		buildID:     buildID,
		fileName:    fileName,
		uploaded:    p.uploaded,
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

// peerBlob implements storage.Blob, reading from the peer orchestrator first.
// The base blob is opened lazily on first fallback and cached so that subsequent
// calls (e.g. Exists then WriteTo) do not re-open the underlying GCS object.
type peerBlob struct {
	client      orchestrator.ChunkServiceClient
	base        storage.StorageProvider
	basePath    string
	baseObjType storage.ObjectType
	buildID     string
	fileName    string
	uploaded    *atomic.Bool

	mu      sync.Mutex
	baseBlb storage.Blob
}

func (b *peerBlob) getOrOpenBase(ctx context.Context) (storage.Blob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.baseBlb != nil {
		return b.baseBlb, nil
	}

	blb, err := b.base.OpenBlob(ctx, b.basePath, b.baseObjType)
	if err != nil {
		return nil, err
	}

	b.baseBlb = blb

	return blb, nil
}

func (b *peerBlob) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	ctx, span := tracer.Start(ctx, "peer-blob-write-to", trace.WithAttributes(
		attribute.String("file_name", b.fileName),
	))
	defer span.End()

	if !b.uploaded.Load() {
		timer := peerReadTimerFactory.Begin(attribute.String(peerOperationAttr, peerOperationWriteTo))

		stream, err := b.client.GetBuildFile(ctx, &orchestrator.GetBuildFileRequest{
			BuildId:  b.buildID,
			FileName: b.fileName,
		})
		if err == nil {
			firstMsg, err := stream.Recv()
			if err == nil && !firstMsg.GetNotAvailable() {
				if firstMsg.GetUseStorage() {
					b.uploaded.Store(true)
				} else {
					n, err := collectStream(dst, firstMsg.GetData(), stream)
					if err != nil {
						span.RecordError(err)
						timer.Failure(ctx, n)

						return n, fmt.Errorf("failed to stream file %q from peer: %w", b.fileName, err)
					}

					span.SetAttributes(attribute.Bool("peer_hit", true))
					timer.Success(ctx, n)

					return n, nil
				}
			}
		}

		timer.Failure(ctx, 0)
	}

	span.SetAttributes(attribute.Bool("peer_hit", false))

	fallback, err := b.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		return 0, err
	}

	return fallback.WriteTo(ctx, dst)
}

func (b *peerBlob) Exists(ctx context.Context) (bool, error) {
	ctx, span := tracer.Start(ctx, "peer-blob-exists", trace.WithAttributes(
		attribute.String("file_name", b.fileName),
	))
	defer span.End()

	if !b.uploaded.Load() {
		timer := peerReadTimerFactory.Begin(attribute.String(peerOperationAttr, peerOperationExists))

		resp, err := b.client.GetBuildFileInfo(ctx, &orchestrator.GetBuildFileInfoRequest{
			BuildId:  b.buildID,
			FileName: b.fileName,
		})
		if err == nil && !resp.GetNotAvailable() {
			if resp.GetUseStorage() {
				b.uploaded.Store(true)
			} else {
				span.SetAttributes(attribute.Bool("peer_hit", true))
				timer.Success(ctx, 0)

				return true, nil
			}
		}

		timer.Failure(ctx, 0)
	}

	span.SetAttributes(attribute.Bool("peer_hit", false))

	fallback, err := b.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		return false, err
	}

	return fallback.Exists(ctx)
}

func (b *peerBlob) Put(ctx context.Context, data []byte) error {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := b.getOrOpenBase(ctx)
	if err != nil {
		return err
	}

	return fallback.Put(ctx, data)
}

// peerSeekable implements storage.Seekable, reading from the peer orchestrator first.
// The base seekable is opened lazily on first fallback and cached so that block-level
// ReadAt calls (thousands per VM boot) do not re-open the underlying GCS object.
// Size is always fetched from the peer because the GCS upload may not have completed.
type peerSeekable struct {
	client      orchestrator.ChunkServiceClient
	base        storage.StorageProvider
	basePath    string
	baseObjType storage.SeekableObjectType
	buildID     string
	fileName    string
	uploaded    *atomic.Bool

	mu      sync.Mutex
	baseDev storage.Seekable
}

func (s *peerSeekable) getOrOpenBase(ctx context.Context) (storage.Seekable, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.baseDev != nil {
		return s.baseDev, nil
	}

	dev, err := s.base.OpenSeekable(ctx, s.basePath, s.baseObjType)
	if err != nil {
		return nil, err
	}

	s.baseDev = dev

	return dev, nil
}

func (s *peerSeekable) Size(ctx context.Context) (int64, error) {
	ctx, span := tracer.Start(ctx, "peer-seekable-size", trace.WithAttributes(
		attribute.String("file_name", s.fileName),
	))
	defer span.End()

	if !s.uploaded.Load() {
		timer := peerReadTimerFactory.Begin(attribute.String(peerOperationAttr, peerOperationSize))

		resp, err := s.client.GetBuildFileInfo(ctx, &orchestrator.GetBuildFileInfoRequest{
			BuildId:  s.buildID,
			FileName: s.fileName,
		})
		if err == nil && !resp.GetNotAvailable() {
			if resp.GetUseStorage() {
				s.uploaded.Store(true)
			} else {
				span.SetAttributes(attribute.Bool("peer_hit", true))
				timer.Success(ctx, 0)

				return resp.GetTotalSize(), nil
			}
		}

		timer.Failure(ctx, 0)
	}

	span.SetAttributes(attribute.Bool("peer_hit", false))

	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		return 0, err
	}

	return fallback.Size(ctx)
}

func (s *peerSeekable) ReadAt(ctx context.Context, buf []byte, off int64) (int, error) {
	ctx, span := tracer.Start(ctx, "peer-seekable-read-at", trace.WithAttributes(
		attribute.String("file_name", s.fileName),
	))
	defer span.End()

	if !s.uploaded.Load() {
		timer := peerReadTimerFactory.Begin(attribute.String(peerOperationAttr, peerOperationReadAt))

		stream, err := s.client.GetBuildFile(ctx, &orchestrator.GetBuildFileRequest{
			BuildId:  s.buildID,
			FileName: s.fileName,
			Offset:   off,
			Length:   int64(len(buf)),
		})
		if err == nil {
			firstMsg, err := stream.Recv()
			if err == nil && !firstMsg.GetNotAvailable() {
				if firstMsg.GetUseStorage() {
					s.uploaded.Store(true)
				} else {
					n := copy(buf, firstMsg.GetData())

					for n < len(buf) {
						msg, recvErr := stream.Recv()
						if errors.Is(recvErr, io.EOF) {
							break
						}
						if recvErr != nil {
							span.RecordError(recvErr)
							timer.Failure(ctx, int64(n))

							return n, fmt.Errorf("failed to receive chunk from peer: %w", recvErr)
						}
						n += copy(buf[n:], msg.GetData())
					}

					if n < len(buf) {
						timer.Failure(ctx, int64(n))

						return n, io.ErrUnexpectedEOF
					}

					span.SetAttributes(attribute.Bool("peer_hit", true))
					timer.Success(ctx, int64(n))

					return n, nil
				}
			}
		}

		timer.Failure(ctx, 0)
	}

	span.SetAttributes(attribute.Bool("peer_hit", false))

	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		return 0, err
	}

	return fallback.ReadAt(ctx, buf, off)
}

func (s *peerSeekable) OpenRangeReader(ctx context.Context, off, length int64) (io.ReadCloser, error) {
	ctx, span := tracer.Start(ctx, "peer-seekable-open-range-reader", trace.WithAttributes(
		attribute.String("file_name", s.fileName),
	))
	defer span.End()

	if !s.uploaded.Load() {
		timer := peerReadTimerFactory.Begin(attribute.String(peerOperationAttr, peerOperationRangeReader))

		streamCtx, cancel := context.WithCancel(ctx)
		stream, err := s.client.GetBuildFile(streamCtx, &orchestrator.GetBuildFileRequest{
			BuildId:  s.buildID,
			FileName: s.fileName,
			Offset:   off,
			Length:   length,
		})
		if err == nil {
			firstMsg, err := stream.Recv()
			if err == nil && !firstMsg.GetNotAvailable() && !firstMsg.GetUseStorage() {
				firstChunk := firstMsg.GetData()

				span.SetAttributes(attribute.Bool("peer_hit", true))
				timer.Success(ctx, int64(len(firstChunk)))

				return &peerStreamReader{
					stream:  stream,
					current: bytes.NewReader(firstChunk),
					cancel:  cancel,
				}, nil
			}

			if firstMsg.GetUseStorage() {
				s.uploaded.Store(true)
			}
		}

		timer.Failure(ctx, 0)
		cancel()
	}

	span.SetAttributes(attribute.Bool("peer_hit", false))

	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		span.RecordError(err)

		return nil, err
	}

	return fallback.OpenRangeReader(ctx, off, length)
}

func (s *peerSeekable) StoreFile(ctx context.Context, path string) error {
	// Writes always go to the base provider (GCS/S3); the peer is read-only.
	fallback, err := s.getOrOpenBase(ctx)
	if err != nil {
		return err
	}

	return fallback.StoreFile(ctx, path)
}

// peerStreamReader wraps a gRPC streaming client as an io.ReadCloser.
// cancel is called on Close to signal the server to terminate the stream.
type peerStreamReader struct {
	stream  orchestrator.ChunkService_GetBuildFileClient
	current *bytes.Reader
	done    bool
	cancel  context.CancelFunc
}

func (r *peerStreamReader) Read(p []byte) (int, error) {
	for {
		if r.current != nil && r.current.Len() > 0 {
			return r.current.Read(p)
		}

		if r.done {
			return 0, io.EOF
		}

		msg, err := r.stream.Recv()
		if errors.Is(err, io.EOF) {
			r.done = true

			return 0, io.EOF
		}
		if err != nil {
			return 0, fmt.Errorf("failed to receive chunk from peer: %w", err)
		}

		r.current = bytes.NewReader(msg.GetData())
	}
}

func (r *peerStreamReader) Close() error {
	r.cancel()

	return nil
}

// collectStream writes firstData and all subsequent stream messages to dst.
func collectStream(dst io.Writer, firstData []byte, stream orchestrator.ChunkService_GetBuildFileClient) (int64, error) {
	n := int64(0)

	if len(firstData) > 0 {
		written, err := dst.Write(firstData)
		n += int64(written)
		if err != nil {
			return n, err
		}
	}

	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, err
		}

		if len(msg.GetData()) > 0 {
			written, err := dst.Write(msg.GetData())
			n += int64(written)
			if err != nil {
				return n, err
			}
		}
	}

	return n, nil
}
