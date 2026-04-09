package middleware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// ErrPanic is returned when a panic is recovered.
var ErrPanic = fmt.Errorf("panic")

// Recovery intercepts panics and converts them to errors.
func Recovery() Interceptor {
	return func(ctx context.Context, op string, _ []any, next func(context.Context) ([]any, error)) (results []any, err error) {
		defer func() {
			if r := recover(); r != nil { //nolint:revive // always called via defer
				logger.L().Error(ctx, fmt.Sprintf("panic in %q nfs operation", op),
					zap.Any("panic", r),
					zap.Stack("stack"),
				)
				err = ErrPanic
			}
		}()

		return next(ctx)
	}
}

// Tracing creates OpenTelemetry spans for each operation.
func Tracing(skipOps map[string]bool) Interceptor {
	tracer := otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware")

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

// Metrics records call counts and durations.
func Metrics(counter metric.Int64Counter, histogram metric.Int64Histogram) Interceptor {
	return func(ctx context.Context, op string, _ []any, next func(context.Context) ([]any, error)) ([]any, error) {
		start := time.Now()
		results, err := next(ctx)
		durationMs := time.Since(start).Milliseconds()

		attrs := metric.WithAttributes(
			attribute.String("operation", op),
			attribute.String("result", classifyResult(err)),
		)
		counter.Add(ctx, 1, attrs)
		histogram.Record(ctx, durationMs, attrs)

		return results, err
	}
}

// Logging logs operation start/end with durations.
func Logging(skipOps map[string]bool) Interceptor {
	return func(ctx context.Context, op string, args []any, next func(context.Context) ([]any, error)) ([]any, error) {
		if skipOps[op] {
			return next(ctx)
		}

		start := time.Now()
		requestID := uuid.NewString()

		l := logger.L().With(zap.String("requestID", requestID))
		l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: start", op), zap.String("operation", op))

		results, err := next(ctx)

		logArgs := []zap.Field{
			zap.Duration("dur", time.Since(start)),
			zap.Any("args", args),
			zap.Any("result", results),
		}

		if err == nil {
			l.Debug(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logArgs...)
		} else {
			logArgs = append(logArgs, zap.Error(err))
			l.Warn(ctx, fmt.Sprintf("[nfs proxy] %s: end", op), logArgs...)
		}

		return results, err
	}
}

func isUserError(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrExist)
}

func classifyResult(err error) string {
	if err == nil {
		return "success"
	}

	if isUserError(err) {
		return "client_error"
	}

	return "other_error"
}

func argsToAttrs(op string, args []any) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	switch op {
	case "FS.Create", "FS.Open", "FS.Stat", "FS.Lstat", "FS.Remove", "FS.Readlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
	case "FS.Rename":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.oldpath", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.newpath", s))
			}
		}
	case "FS.OpenFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
		if len(args) > 1 {
			if flag, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.flag", flag))
			}
		}
		if len(args) > 2 {
			if perm, ok := args[2].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.perm", perm.String()))
			}
		}
	case "FS.TempFile":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.dir", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.prefix", s))
			}
		}
	case "FS.ReadDir", "FS.Chroot":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.path", s))
			}
		}
	case "FS.MkdirAll":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.filename", s))
			}
		}
		if len(args) > 1 {
			if perm, ok := args[1].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.perm", perm.String()))
			}
		}
	case "FS.Symlink":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.target", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.link", s))
			}
		}
	case "File.Write", "File.Read":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				attrs = append(attrs, attribute.Int("nfs.len", len(p)))
			}
		}
	case "File.ReadAt":
		if len(args) > 0 {
			if p, ok := args[0].([]byte); ok {
				attrs = append(attrs, attribute.Int("nfs.len", len(p)))
			}
		}
		if len(args) > 1 {
			if offset, ok := args[1].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.offset", offset))
			}
		}
	case "File.Seek":
		if len(args) > 0 {
			if offset, ok := args[0].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.offset", offset))
			}
		}
		if len(args) > 1 {
			if whence, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.whence", whence))
			}
		}
	case "File.Truncate":
		if len(args) > 0 {
			if size, ok := args[0].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.size", size))
			}
		}
	case "Change.Chmod":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if mode, ok := args[1].(os.FileMode); ok {
				attrs = append(attrs, attribute.String("nfs.mode", mode.String()))
			}
		}
	case "Change.Lchown", "Change.Chown":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if uid, ok := args[1].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.uid", uid))
			}
		}
		if len(args) > 2 {
			if gid, ok := args[2].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.gid", gid))
			}
		}
	case "Change.Chtimes":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("nfs.name", s))
			}
		}
		if len(args) > 1 {
			if atime, ok := args[1].(time.Time); ok {
				attrs = append(attrs, attribute.String("nfs.atime", atime.String()))
			}
		}
		if len(args) > 2 {
			if mtime, ok := args[2].(time.Time); ok {
				attrs = append(attrs, attribute.String("nfs.mtime", mtime.String()))
			}
		}
	case "Handler.Mount":
		if len(args) > 0 {
			if s, ok := args[0].(string); ok {
				attrs = append(attrs, attribute.String("net.conn.remote_addr", s))
			}
		}
		if len(args) > 1 {
			if s, ok := args[1].(string); ok {
				attrs = append(attrs, attribute.String("nfs.mount.dirpath", s))
			}
		}
	case "Handler.ToHandle":
		if len(args) > 0 {
			if paths, ok := args[0].([]string); ok {
				attrs = append(attrs, attribute.StringSlice("nfs.path", paths))
			}
		}
	}

	return attrs
}

func resultsToAttrs(op string, results []any) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	switch op {
	case "File.Write", "File.Read", "File.ReadAt":
		if len(results) > 0 {
			if n, ok := results[0].(int); ok {
				attrs = append(attrs, attribute.Int("nfs.n", n))
			}
		}
	case "File.Seek":
		if len(results) > 0 {
			if n, ok := results[0].(int64); ok {
				attrs = append(attrs, attribute.Int64("nfs.n", n))
			}
		}
	}

	return attrs
}
