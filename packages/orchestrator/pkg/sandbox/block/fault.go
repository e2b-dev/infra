//go:build linux

package block

import (
	"errors"
)

// ErrMemoryFault reports that accessing a memory-mapped file raised a memory
// fault (SIGBUS/SIGSEGV at a non-nil address). The typical cause is the
// backing storage failing with an unrecoverable read error (bad sector) when
// the kernel pages in a mapped range - the kernel cannot return EIO for a
// plain memory access, so it delivers a signal instead. A file truncated
// while mapped faults the same way.
var ErrMemoryFault = errors.New("memory fault while accessing memory-mapped file")

// RunFaultSafe runs fn, converting a runtime memory fault raised by fn (e.g.
// reading a memory-mapped file whose backing block is unreadable) into an
// error wrapping ErrMemoryFault instead of terminating the process.
//
// Any other panic from fn — including nil-pointer dereferences — propagates
// unchanged: those are program bugs and must stay loud.
//
// After a fault the destination buffer of an interrupted copy may be
// partially written; callers must treat ErrMemoryFault as a total failure of
// the operation.
func RunFaultSafe(fn func() error) error {
	// TODO: convert memory faults via debug.SetPanicOnFault + recover.
	// Pass-through skeleton so the fault tests demonstrate the current
	// (process-killing) behavior first.
	return fn()
}
