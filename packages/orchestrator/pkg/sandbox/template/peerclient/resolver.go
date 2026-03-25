package peerclient

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const peerConnectTimeout = 5 * time.Second

// Resolver looks up peer addresses for build IDs and manages gRPC connections
// to peer orchestrators. It is used by the routing provider to decide, per
// storage path, whether to read from a peer or from the base provider.
//
// The unexported resolve method restricts implementations to this package.
type Resolver interface {
	resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult)
	Purge(buildID string)
	Close()
}

// UploadedHeaders holds the serialized V4 headers received from the peer's
// use_storage response. These are used by build.File to atomically swap headers
// when transitioning from P2P to compressed GCS reads.
type UploadedHeaders struct {
	MemfileHeader []byte
	RootfsHeader  []byte
}

type resolveResult struct {
	client   orchestrator.ChunkServiceClient
	uploaded *atomic.Pointer[UploadedHeaders]
	addr     string
}

// NopResolver returns a Resolver that always falls back to the base provider.
func NopResolver() Resolver { return nopResolver{} }

type nopResolver struct{}

func (nopResolver) resolve(context.Context, string) (attribute.KeyValue, resolveResult) {
	return attrResolveNoPeer, resolveResult{}
}
func (nopResolver) Purge(string) {}
func (nopResolver) Close()       {}

// peerResolver is the real implementation that looks up peers via the Registry.
type peerResolver struct {
	registry    Registry
	selfAddress string
	peerConns   sync.Map // address → *grpc.ClientConn
	uploaded    sync.Map // buildID → *atomic.Pointer[UploadedHeaders]
	dialGroup   singleflight.Group
}

func NewResolver(registry Registry, selfAddress string) Resolver {
	return &peerResolver{
		registry:    registry,
		selfAddress: selfAddress,
	}
}

func (r *peerResolver) readPeerAddress(ctx context.Context, buildID string) (string, bool, error) {
	return r.registry.Lookup(ctx, buildID)
}

// getOrDialPeer deduplicates concurrent dials via singleflight.
func (r *peerResolver) getOrDialPeer(address string) (*grpc.ClientConn, error) {
	if conn, ok := r.peerConns.Load(address); ok {
		return conn.(*grpc.ClientConn), nil
	}

	v, err, _ := r.dialGroup.Do(address, func() (any, error) {
		if conn, ok := r.peerConns.Load(address); ok {
			return conn, nil
		}

		conn, err := grpc.NewClient(address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: peerConnectTimeout}),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to dial peer %s: %w", address, err)
		}

		r.peerConns.Store(address, conn)

		return conn, nil
	})
	if err != nil {
		return nil, err
	}

	return v.(*grpc.ClientConn), nil
}

func (r *peerResolver) isSelfAddress(address string) bool {
	return address == r.selfAddress
}

// uploadedPtr returns a shared atomic pointer for the given build ID.
// Non-nil value means the build is uploaded (use_storage). The UploadedHeaders
// may contain serialized V4 headers for the peer transition protocol, or be
// empty (for uncompressed builds).
func (r *peerResolver) uploadedPtr(buildID string) *atomic.Pointer[UploadedHeaders] {
	if v, ok := r.uploaded.Load(buildID); ok {
		return v.(*atomic.Pointer[UploadedHeaders])
	}

	ptr := &atomic.Pointer[UploadedHeaders]{}
	actual, _ := r.uploaded.LoadOrStore(buildID, ptr)

	return actual.(*atomic.Pointer[UploadedHeaders])
}

// Purge removes the uploaded state for a build, called on template
// cache eviction so the entry doesn't accumulate forever.
func (r *peerResolver) Purge(buildID string) {
	r.uploaded.Delete(buildID)
}

// resolve looks up the peer for the given build and returns a gRPC client if
// a remote peer is found. Returns a nil client when the base provider should
// be used instead (uploaded, no peer, self, or error).
func (r *peerResolver) resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult) {
	hdrs := r.uploadedPtr(buildID)
	if hdrs.Load() != nil {
		return attrResolveUploaded, resolveResult{}
	}

	addr, found, err := r.readPeerAddress(ctx, buildID)
	if err != nil {
		return attrResolveRedisError, resolveResult{}
	}

	if !found {
		return attrResolveNoPeer, resolveResult{}
	}

	if r.isSelfAddress(addr) {
		return attrResolveSelf, resolveResult{}
	}

	conn, err := r.getOrDialPeer(addr)
	if err != nil {
		return attrResolveDialError, resolveResult{}
	}

	return attrResolvePeer, resolveResult{
		client:   orchestrator.NewChunkServiceClient(conn),
		uploaded: hdrs,
		addr:     addr,
	}
}

func (r *peerResolver) Close() {
	r.peerConns.Range(func(_, value any) bool {
		_ = value.(*grpc.ClientConn).Close()

		return true
	})
}
