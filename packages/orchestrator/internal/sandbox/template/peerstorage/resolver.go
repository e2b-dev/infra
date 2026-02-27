package peerstorage

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
type Resolver struct {
	registry           Registry
	excludePeerAddress string
	peerConns          sync.Map // address → *grpc.ClientConn
	uploadedBuilds     sync.Map // buildID → *atomic.Bool
	dialGroup          singleflight.Group
}

func NewResolver(registry Registry, excludePeerAddress string) *Resolver {
	return &Resolver{
		registry:           registry,
		excludePeerAddress: excludePeerAddress,
	}
}

func (r *Resolver) readPeerAddress(ctx context.Context, buildID string) (string, bool, error) {
	return r.registry.Lookup(ctx, buildID)
}

// getOrDialPeer deduplicates concurrent dials via singleflight.
func (r *Resolver) getOrDialPeer(address string) (*grpc.ClientConn, error) {
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

func (r *Resolver) isSelfAddress(address string) bool {
	return address == r.excludePeerAddress
}

// uploadedFlag returns a shared atomic flag for the given build ID.
// Once any reader sets the flag (via use_storage), all subsequent opens for
// that build skip the peer.
func (r *Resolver) uploadedFlag(buildID string) *atomic.Bool {
	if v, ok := r.uploadedBuilds.Load(buildID); ok {
		return v.(*atomic.Bool)
	}

	flag := &atomic.Bool{}
	actual, _ := r.uploadedBuilds.LoadOrStore(buildID, flag)

	return actual.(*atomic.Bool)
}

// PurgeUploaded removes the uploaded state for a build, called on template
// cache eviction so the entry doesn't accumulate forever.
func (r *Resolver) PurgeUploaded(buildID string) {
	r.uploadedBuilds.Delete(buildID)
}

type resolveResult struct {
	client   orchestrator.ChunkServiceClient
	uploaded *atomic.Bool
	addr     string
}

// resolve looks up the peer for the given build and returns a gRPC client if
// a remote peer is found. Returns a nil client when the base provider should
// be used instead (uploaded, no peer, self, or error).
func (r *Resolver) resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult) {
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

	conn, err := r.getOrDialPeer(addr)
	if err != nil {
		return attrResolveDialError, resolveResult{}
	}

	return attrResolvePeer, resolveResult{
		client:   orchestrator.NewChunkServiceClient(conn),
		uploaded: uploaded,
		addr:     addr,
	}
}

func (r *Resolver) Close() {
	r.peerConns.Range(func(_, value any) bool {
		_ = value.(*grpc.ClientConn).Close()

		return true
	})
}
