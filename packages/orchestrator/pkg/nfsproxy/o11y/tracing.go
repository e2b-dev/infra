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

	return func(ctx context.Context, req middleware.Request, next func(context.Context) error) (err error) {
		op := req.Op()
		if skipOps[op] {
			return next(ctx)
		}

		ctx, span := tracer.Start(ctx, op, trace.WithAttributes(argsToAttrs(req)...))
		defer func() {
			if err != nil {
				span.RecordError(err)
				if !isUserError(err) {
					span.SetStatus(codes.Error, err.Error())
				}
			}
			span.End()
		}()

		return next(ctx)
	}
}
