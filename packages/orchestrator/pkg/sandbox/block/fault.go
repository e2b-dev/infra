//go:build linux

package block

import (
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
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
func RunFaultSafe(fn func() error) (err error) {
	// SetPanicOnFault is per-goroutine: a fault in fn on this goroutine
	// becomes a recoverable panic instead of an unrecoverable fatal throw.
	// The cost on the happy path is a bool swap on the g struct plus two
	// deferred calls — negligible next to the copies this guards.
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Faults surfaced by SetPanicOnFault carry the fault address via an
		// Addr method (see runtime/debug.SetPanicOnFault docs); no other
		// runtime.Error has it. Anything else is a program bug: re-panic.
		re, isRuntimeErr := r.(runtime.Error)
		if !isRuntimeErr {
			panic(r)
		}
		if _, hasAddr := r.(interface{ Addr() uintptr }); !hasAddr {
			panic(r)
		}
		err = fmt.Errorf("%w: %v", ErrMemoryFault, re)
	}()

	return fn()
}
