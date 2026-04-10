package o11y

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
)

// Tracing creates OpenTelemetry spans for each operation.
func Tracing(skipOps map[string]bool) middleware.Interceptor {
	tracer := otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/o11y")

	return func(ctx context.Context, op string, args []any, next func(context.Context) ([]any, error)) ([]any, error) {
		if skipOps[op] {
			return next(ctx)
		}

		ctx, span := tracer.Start(ctx, op, trace.WithAttributes(argsToAttrs(op, args)...)) //nolint:spancheck // span.End called below
		results, err := next(ctx)
		if err != nil {
			span.RecordError(err)
			if !isUserError(err) {
				span.SetStatus(codes.Error, err.Error())
			}
		}
		span.SetAttributes(resultsToAttrs(op, results)...)
		span.End()

		return results, err
	}
}
