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

// A non-zero rate usually means the node's local disk is failing and the
// node should be drained.
var memoryFaultCounter = utils.Must(meter.Int64Counter(
	"orchestrator.block.memory_fault",
	metric.WithDescription("Memory faults recovered while accessing memory-mapped cache files."),
	metric.WithUnit("{fault}"),
))

// ErrMemoryFault is the errors.Is match target for memory faults recovered by
// RunFaultSafe; the concrete type is *MemoryFaultError. Such faults are
// typically raised by an unrecoverable read error (bad sector) under a
// memory-mapped file: the kernel cannot return EIO for a plain memory access,
// so it delivers SIGBUS instead.
var ErrMemoryFault = errors.New("memory fault while accessing memory-mapped file")

// MemoryFaultError is returned by RunFaultSafe for a recovered memory fault.
type MemoryFaultError struct {
	// Addr is the faulting address, best-effort (see
	// runtime/debug.SetPanicOnFault).
	Addr uintptr

	cause error // the runtime.Error the fault panic carried
}

func (e *MemoryFaultError) Error() string {
	return fmt.Sprintf("memory fault while accessing memory-mapped file at address 0x%x: %v", e.Addr, e.cause)
}

func (e *MemoryFaultError) Is(target error) bool { return target == ErrMemoryFault }

func (e *MemoryFaultError) Unwrap() error { return e.cause }

// RunFaultSafe runs fn, converting a memory fault raised by fn (e.g. reading
// a memory-mapped file whose backing block is unreadable) into a
// *MemoryFaultError instead of terminating the process. Any other panic —
// including nil-pointer dereferences — propagates unchanged.
//
// After a fault the destination buffer of an interrupted copy may be
// partially written; callers must treat the error as a total failure.
func RunFaultSafe(ctx context.Context, fn func() error) (err error) {
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Only fault panics carry an Addr method (see
		// runtime/debug.SetPanicOnFault); anything else is a bug: re-panic.
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
