package peerclient

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

const peerConnectTimeout = 5 * time.Second

// Resolver looks up peer addresses for build IDs and manages gRPC connections
// to peer orchestrators. It is used by the routing provider to decide, per
// storage path, whether to read from a peer or from the base provider.
//
// The unexported resolve method restricts implementations to this package.
type Resolver interface {
	UploadChecker
	resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult)
	Purge(buildID string)
	Close()
}

// UploadChecker answers whether a build's source upload is still in flight on
// a peer orchestrator. False means the build is finalized in remote storage
// (or was never registered as a peer to begin with) and is therefore safe to
// read directly from the base provider. The narrow surface lets consumers
// (e.g. sandbox.Uploads) take a dependency on upload state without pulling in
// the full peer routing API.
type UploadChecker interface {
	IsUploading(ctx context.Context, buildID string) bool
}

type resolveResult struct {
	client   orchestrator.ChunkServiceClient
	uploaded *atomic.Bool
	addr     string
}

// NopResolver returns a Resolver that always falls back to the base provider.
func NopResolver() Resolver { return nopResolver{} }

type nopResolver struct{}

func (nopResolver) resolve(context.Context, string) (attribute.KeyValue, resolveResult) {
	return attrResolveNoPeer, resolveResult{}
}
func (nopResolver) IsUploading(context.Context, string) bool { return false }
func (nopResolver) Purge(string)                             {}
func (nopResolver) Close()                                   {}

// peerResolver is the real implementation that looks up peers via the Registry.
type peerResolver struct {
	registry       Registry
	selfAddress    string
	peerConns      sync.Map // address → *grpc.ClientConn
	uploadedBuilds sync.Map // buildID → *atomic.Bool
	dialGroup      singleflight.Group
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
func (r *peerResolver) getOrDialPeer(ctx context.Context, address string) (*grpc.ClientConn, error) {
	if conn, ok := r.peerConns.Load(address); ok {
		return conn.(*grpc.ClientConn), nil
	}

	v, err, _ := r.dialGroup.Do(address, func() (any, error) {
		if conn, ok := r.peerConns.Load(address); ok {
			return conn, nil
		}

		conn, err := grpc.NewClient(address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
			grpc.WithConnectParams(grpc.ConnectParams{MinConnectTimeout: peerConnectTimeout}),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to dial peer %s: %w", address, err)
		}

		e2bgrpc.ObserveConnection(ctx, conn, "peer-orchestrator")

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

// uploadedFlag returns a shared atomic flag for the given build ID.
// Once any reader sets the flag (via use_storage), all subsequent opens for
// that build skip the peer.
func (r *peerResolver) uploadedFlag(buildID string) *atomic.Bool {
	if v, ok := r.uploadedBuilds.Load(buildID); ok {
		return v.(*atomic.Bool)
	}

	flag := &atomic.Bool{}
	actual, _ := r.uploadedBuilds.LoadOrStore(buildID, flag)

	return actual.(*atomic.Bool)
}

// Purge removes the uploaded state for a build, called on template
// cache eviction so the entry doesn't accumulate forever.
func (r *peerResolver) Purge(buildID string) {
	r.uploadedBuilds.Delete(buildID)
}

// resolve looks up the peer for the given build and returns a gRPC client if
// a remote peer is found. Returns a nil client when the base provider should
// be used instead (uploaded, no peer, self, or error).
func (r *peerResolver) resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult) {
	uploaded := r.uploadedFlag(buildID)
	if uploaded.Load() {
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

	conn, err := r.getOrDialPeer(ctx, addr)
	if err != nil {
		return attrResolveDialError, resolveResult{}
	}

	return attrResolvePeer, resolveResult{
		client:   orchestrator.NewChunkServiceClient(conn),
		uploaded: uploaded,
		addr:     addr,
	}
}

func (r *peerResolver) IsUploading(ctx context.Context, buildID string) bool {
	status, _ := r.resolve(ctx, buildID)

	return status == attrResolvePeer
}

func (r *peerResolver) Close() {
	r.peerConns.Range(func(_, value any) bool {
		_ = value.(*grpc.ClientConn).Close()

		return true
	})
}
