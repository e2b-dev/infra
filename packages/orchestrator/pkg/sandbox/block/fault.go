//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block")

// memoryFaultCounter counts recovered memory faults across all RunFaultSafe
// call sites (build segment reads, NBD dispatch, dedup). A non-zero rate on a
// node usually means its local disk is failing and it should be drained.
var memoryFaultCounter = utils.Must(meter.Int64Counter(
	"orchestrator.block.memory_fault",
	metric.WithDescription("Memory faults recovered while accessing memory-mapped cache files."),
	metric.WithUnit("{fault}"),
))

// ErrMemoryFault is the errors.Is match target for memory faults recovered by
// RunFaultSafe; the concrete returned type is *MemoryFaultError. A memory
// fault (SIGBUS/SIGSEGV at a non-nil address) is typically raised when the
// backing storage fails with an unrecoverable read error (bad sector) while
// the kernel pages in a mapped range — the kernel cannot return EIO for a
// plain memory access, so it delivers a signal instead. A file truncated
// while mapped faults the same way.
var ErrMemoryFault = errors.New("memory fault while accessing memory-mapped file")

// MemoryFaultError is the concrete error RunFaultSafe returns for a recovered
// memory fault. Match it with errors.Is(err, ErrMemoryFault), or extract the
// fault address with errors.As.
type MemoryFaultError struct {
	// Addr is the faulting address, best-effort (see
	// runtime/debug.SetPanicOnFault). It can be correlated with the mmap
	// layout to find the file offset — and hence the disk block — that
	// failed.
	Addr uintptr

	cause error // the runtime.Error the fault panic carried
}

func (e *MemoryFaultError) Error() string {
	return fmt.Sprintf("memory fault while accessing memory-mapped file at address 0x%x: %v", e.Addr, e.cause)
}

// Is makes errors.Is(err, ErrMemoryFault) match without callers needing the
// concrete type.
func (e *MemoryFaultError) Is(target error) bool { return target == ErrMemoryFault }

func (e *MemoryFaultError) Unwrap() error { return e.cause }

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
func RunFaultSafe(ctx context.Context, fn func() error) (err error) {
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
		addrErr, hasAddr := r.(interface{ Addr() uintptr })
		if !hasAddr {
			panic(r)
		}
		memoryFaultCounter.Add(ctx, 1)
		err = &MemoryFaultError{Addr: addrErr.Addr(), cause: re}
	}()

	return fn()
}
