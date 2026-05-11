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
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const peerConnectTimeout = 5 * time.Second

// Resolver looks up peer addresses for build IDs and manages gRPC connections
// to peer orchestrators. It is used by the routing provider to decide, per
// storage path, whether to read from a peer or from the base provider.
//
// The unexported resolve method restricts implementations to this package.
type Resolver interface {
	resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult)
	IsActive(buildID string) bool
	PendingHeader(buildID, name string) *header.Header
	Purge(buildID string)
	Close()
}

// peerState is per-buildID state shared across every peer{Blob,Seekable}.
// uploaded routes future reads to base; memfile/rootfs hold V4 headers
// delivered in UseStorage responses for build.File to install.
type peerState struct {
	uploaded atomic.Bool
	memfile  atomic.Pointer[header.Header]
	rootfs   atomic.Pointer[header.Header]
}

func (b *peerState) setHeader(name string, h *header.Header) {
	switch name {
	case storage.MemfileName:
		b.memfile.Store(h)
	case storage.RootfsName:
		b.rootfs.Store(h)
	}
}

func (b *peerState) header(name string) *header.Header {
	// Gate on uploaded: writer sequences setHeader before uploaded.Store(true);
	// a reader observing uploaded=true is guaranteed (Go atomic ordering) to
	// see the prior header store on its subsequent atomic load. V3 stores
	// nothing — header() correctly returns nil for those builds.
	if !b.uploaded.Load() {
		return nil
	}
	switch name {
	case storage.MemfileName:
		return b.memfile.Load()
	case storage.RootfsName:
		return b.rootfs.Load()
	}

	return nil
}

type resolveResult struct {
	client orchestrator.ChunkServiceClient
	state  *peerState
	addr   string
}

// NopResolver returns a Resolver that always falls back to the base provider.
func NopResolver() Resolver { return nopResolver{} }

type nopResolver struct{}

func (nopResolver) resolve(context.Context, string) (attribute.KeyValue, resolveResult) {
	return attrResolveNoPeer, resolveResult{}
}
func (nopResolver) IsActive(string) bool                        { return false }
func (nopResolver) PendingHeader(string, string) *header.Header { return nil }
func (nopResolver) Purge(string)                                {}
func (nopResolver) Close()                                      {}

// peerResolver is the real implementation that looks up peers via the Registry.
type peerResolver struct {
	registry       Registry
	selfAddress    string
	peerConns      sync.Map // address → *grpc.ClientConn
	uploadedBuilds sync.Map // buildID → *peerState
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

// peerState returns the shared per-build state, creating it on first call.
// The presence of an entry in builds means "this build is/was peer-served on
// this orch"; the uploaded flag tracks whether a reader has observed the
// source switch to storage. Only resolve() creates entries (in the peer-found
// branch) so absence is meaningful: no peer ever existed for this build from
// this orch's perspective.
func (r *peerResolver) peerState(buildID string) *peerState {
	actual, _ := r.uploadedBuilds.LoadOrStore(buildID, &peerState{})

	return actual.(*peerState)
}

// Purge removes the per-build state, called on template cache eviction so
// the entry doesn't accumulate forever.
func (r *peerResolver) Purge(buildID string) {
	r.uploadedBuilds.Delete(buildID)
}

// resolve looks up the peer for the given build and returns a gRPC client if
// a remote peer is found. Returns a zero result when the base provider should
// be used instead (uploaded, no peer, self, or error).
func (r *peerResolver) resolve(ctx context.Context, buildID string) (attribute.KeyValue, resolveResult) {
	// Fast path: a prior resolve flagged this build as peer-served and a
	// reader has since observed the switch to storage.
	if v, ok := r.uploadedBuilds.Load(buildID); ok && v.(*peerState).uploaded.Load() {
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

	// Peer found and dialable — register state now so IsActive and future
	// resolves can answer locally without touching Redis.
	return attrResolvePeer, resolveResult{
		client: orchestrator.NewChunkServiceClient(conn),
		state:  r.peerState(buildID),
		addr:   addr,
	}
}

// IsActive reports whether a peer is currently serving this build's
// chunks on this orch — i.e., resolve() found a peer and no reader has yet
// observed the switch to storage. Pure local read; no Redis, no dial.
//
// Absence of an entry means no peer was ever seen for this build, which
// implies the build is durable in storage.
func (r *peerResolver) IsActive(buildID string) bool {
	v, ok := r.uploadedBuilds.Load(buildID)

	return ok && !v.(*peerState).uploaded.Load()
}

func (r *peerResolver) PendingHeader(buildID, name string) *header.Header {
	v, ok := r.uploadedBuilds.Load(buildID)
	if !ok {
		return nil
	}

	return v.(*peerState).header(name)
}

func (r *peerResolver) Close() {
	r.peerConns.Range(func(_, value any) bool {
		_ = value.(*grpc.ClientConn).Close()

		return true
	})
}
