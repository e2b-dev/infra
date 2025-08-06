package layer

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
)

// FunctionAction wraps a function to implement LayerActionExecutor
type FunctionAction struct {
	fn func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error)
}

func NewFunctionAction(fn func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error)) ActionExecutor {
	return &FunctionAction{fn: fn}
}

func (e *FunctionAction) Execute(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error) {
	return e.fn(ctx, sbx)
}
