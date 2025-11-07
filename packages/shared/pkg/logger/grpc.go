package logger

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.uber.org/zap"
)

const HealthCheckRoute = "/grpc.health.v1.Health/Check"

func GRPCLogger(l *zap.Logger) logging.Logger {
	return logging.LoggerFunc(func(_ context.Context, lvl logging.Level, msg string, fields ...any) {
		ignoredFields := map[string]struct{}{
			"grpc.request.content":  {},
			"grpc.response.content": {},
		}

		w := 0
		for i := 0; i+1 < len(fields); i += 2 {
			k, ok := fields[i].(string)
			if !ok {
				continue
			}

			if _, drop := ignoredFields[k]; drop {
				continue
			}

			fields[w], fields[w+1] = k, fields[i+1]
			w += 2
		}
		fields = fields[:w]

		f := make([]zap.Field, 0, len(fields)/2)

		methodFullNameMap := map[string]string{
			"grpc.service":     "...",
			"grpc.method":      "...",
			"grpc.method_type": "...",
			"grpc.code":        "-",
		}

		for i := 0; i < len(fields)-1; i += 2 {
			key := fields[i].(string)

			if _, ok := ignoredFields[key]; ok {
				continue
			}

			value := fields[i+1]

			switch v := value.(type) {
			case string:
				f = append(f, zap.String(key, v))

				_, ok := methodFullNameMap[key]
				if ok {
					methodFullNameMap[key] = v
				}
			case int:
				f = append(f, zap.Int(key, v))
			case bool:
				f = append(f, zap.Bool(key, v))
			default:
				f = append(f, zap.Any(key, v))
			}
		}

		logger := l.WithOptions(zap.AddCallerSkip(1)).With(f...)

		methodFullName := fmt.Sprintf("%s/%s/%s",
			methodFullNameMap["grpc.service"],
			methodFullNameMap["grpc.method"],
			methodFullNameMap["grpc.method_type"],
		)
		if msg == "finished call" || msg == "finished streaming call" {
			methodFullName = fmt.Sprintf("%s [%s]", methodFullName, methodFullNameMap["grpc.code"])
		}

		message := fmt.Sprintf("%s: %s", methodFullName, msg)

		switch lvl {
		case logging.LevelDebug:
			logger.Debug(message)
		case logging.LevelInfo:
			logger.Info(message)
		case logging.LevelWarn:
			logger.Warn(message)
		case logging.LevelError:
			logger.Error(message)
		default:
			panic(fmt.Sprintf("unknown level %v", lvl))
		}
	})
}

func WithoutHealthCheck() selector.Matcher {
	return WithoutRoutes(HealthCheckRoute)
}

func WithoutRoutes(routes ...string) selector.Matcher {
	return selector.MatchFunc(func(_ context.Context, c interceptors.CallMeta) bool {
		for _, route := range routes {
			if c.FullMethod() == route {
				return false
			}
		}

		return true
	})
}
