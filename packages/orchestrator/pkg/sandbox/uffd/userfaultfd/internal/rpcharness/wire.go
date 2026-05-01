// Package rpcharness provides the wire types, typed RPC client, and
// barrier registry shared between the parent and child halves of the
// userfaultfd test harness. It deliberately knows nothing about
// *Userfaultfd internals so it can sit in a sibling internal package
// and never get pulled into a production import path.
package rpcharness

// Empty is the stand-in args/reply type for net/rpc methods that take
// or return nothing meaningful. net/rpc still requires both pointers
// to be exported.
type Empty struct{}

// BootstrapArgs is the single message the parent sends to drive
// child initialisation. Everything that used to flow over env vars or
// the content tmp file lives in this struct, base64-encoded by the
// JSON-RPC codec for byte slices. For typical test sizes (≤1MB) the
// encoding overhead is irrelevant; if a future test needs >10MB of
// source content, that PR can revisit and add chunking.
type BootstrapArgs struct {
	MmapStart uint64
	Pagesize  int64
	TotalSize int64
	AlwaysWP  bool
	// Barriers gates the test-only worker hooks. Off by default so
	// the worker hot path stays a single nil-pointer load + branch
	// in non-race tests.
	Barriers bool
	Content  []byte
}

// BootstrapReply is empty today; kept as a named type so a future
// reply field can be added without touching every call site.
type BootstrapReply struct{}

// PageStateEntry is the wire format for the Paging.States RPC.
// State is the raw uint8 value of the parent package's pageState
// enum; the parent translates back at the boundary.
type PageStateEntry struct {
	State  uint8
	Offset uint64
}

// PageStatesReply carries the snapshot returned by Paging.States.
type PageStatesReply struct {
	Entries []PageStateEntry
}

// FaultBarrierArgs requests installation of a barrier at (Addr, Point).
type FaultBarrierArgs struct {
	Addr  uint64
	Point uint8
}

// FaultBarrierReply carries the opaque token used by subsequent
// WaitHeld / Release calls.
type FaultBarrierReply struct {
	Token uint64
}

// TokenArgs carries the opaque token returned by FaultBarrierReply.
type TokenArgs struct {
	Token uint64
}
