// Package testharness provides the wire types, typed RPC client, and
// barrier registry shared between the parent and child halves of the
// userfaultfd test harness.
package testharness

// Empty is the placeholder for net/rpc methods that take or return
// nothing; net/rpc still requires both args and reply pointers.
type Empty struct{}

type BootstrapArgs struct {
	MmapStart uint64
	Pagesize  int64
	TotalSize int64
	AlwaysWP  bool
	// Barriers gates the test-only worker hooks (off by default).
	Barriers bool
	Content  []byte
}

type BootstrapReply struct{}

// PageStateEntry is the wire form of a block.State for a single page offset.
type PageStateEntry struct {
	State  uint8
	Offset uint64
}

type PageStatesReply struct {
	Entries []PageStateEntry
}

type FaultBarrierArgs struct {
	Addr  uint64
	Point uint8
}

type FaultBarrierReply struct {
	Token uint64
}

type TokenArgs struct {
	Token uint64
}

// FaultOffsetsReply carries the memfile offsets of dequeued fault events, in
// dequeue order.
type FaultOffsetsReply struct {
	Offsets []int64
}

// CoWBeginArgs installs a CoW export window over the given page indices.
type CoWBeginArgs struct {
	Pages []uint64
}

// CoWStateReply is a snapshot of the child's active (or finished) window.
type CoWStateReply struct {
	Copied      int64
	Canceled    bool
	CancelCause string
	// Sink is the identity-offset sink contents (pre-images captured so far;
	// unwritten ranges are zero). Sized like the guest memory — fine at test
	// scale, and consistent with Bootstrap shipping Content the same way.
	Sink []byte
}

// CoWSweepReply carries the sweep + uninstall outcome.
type CoWSweepReply struct {
	SweepError  string
	CancelCause string
}
