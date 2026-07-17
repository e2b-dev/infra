//go:build linux

package block

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block")

var memoryFaultCounter = utils.Must(meter.Int64Counter(
	"orchestrator.block.memory_fault",
	metric.WithDescription("Memory faults recovered while accessing memory-mapped cache files."),
	metric.WithUnit("{fault}"),
))

// MemoryFaultError is a memory fault recovered by RunFaultSafe. The typical
// cause is an unrecoverable read error (bad sector) under a memory-mapped
// file: the kernel cannot return EIO for a memory access, so it delivers
// SIGBUS instead.
type MemoryFaultError struct {
	Addr  uintptr // faulting address, best-effort
	cause error
}

func (e *MemoryFaultError) Error() string {
	return fmt.Sprintf("memory fault while accessing memory-mapped file at address 0x%x: %v", e.Addr, e.cause)
}

func (e *MemoryFaultError) Unwrap() error { return e.cause }

// RunFaultSafe runs fn, converting a memory fault into a *MemoryFaultError
// instead of a process-killing runtime throw. Other panics propagate. A
// faulted copy may have partially written its destination.
func RunFaultSafe(ctx context.Context, fn func() error) (err error) {
	old := debug.SetPanicOnFault(true)
	defer debug.SetPanicOnFault(old)
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		// Only fault panics carry Addr; anything else is a bug.
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
