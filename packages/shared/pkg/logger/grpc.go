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
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		f := make([]zap.Field, 0, len(fields)/2)

		methodFullNameMap := map[string]string{
			"grpc.service":     "...",
			"grpc.method":      "...",
			"grpc.method_type": "...",
			"grpc.code":        "-",
		}

		for i := 0; i < len(fields)-1; i += 2 {
			key := fields[i]
			value := fields[i+1]

			switch v := value.(type) {
			case string:
				f = append(f, zap.String(key.(string), v))

				_, ok := methodFullNameMap[key.(string)]
				if ok {
					methodFullNameMap[key.(string)] = v
				}
			case int:
				f = append(f, zap.Int(key.(string), v))
			case bool:
				f = append(f, zap.Bool(key.(string), v))
			default:
				f = append(f, zap.Any(key.(string), v))
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
