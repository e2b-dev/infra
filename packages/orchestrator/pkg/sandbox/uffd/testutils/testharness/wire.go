// Package testharness provides the wire types, typed RPC client, and
// barrier registry shared between the parent and child halves of the
// userfaultfd test harness. It deliberately knows nothing about
// *Userfaultfd internals so it can sit alongside the other uffd test
// utilities and never get pulled into a production import path.
package testharness

// Empty stands in for net/rpc methods that take or return nothing;
// net/rpc still requires both args and reply to be exported pointers.
type Empty struct{}

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

type BootstrapReply struct{}

// PageStateEntry is the wire form of the parent package's pageState
// enum; the parent translates back at the boundary.
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
