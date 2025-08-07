package layer

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
)

type FunctionActionFn func(ctx context.Context, sbx *sandbox.Sandbox, cmdMeta sandboxtools.CommandMetadata) (sandboxtools.CommandMetadata, error)

type FunctionAction struct {
	fn FunctionActionFn
}

func NewFunctionAction(fn FunctionActionFn) ActionExecutor {
	return &FunctionAction{fn: fn}
}

func (e *FunctionAction) Execute(ctx context.Context, sbx *sandbox.Sandbox, cmdMeta sandboxtools.CommandMetadata) (sandboxtools.CommandMetadata, error) {
	return e.fn(ctx, sbx, cmdMeta)
}
