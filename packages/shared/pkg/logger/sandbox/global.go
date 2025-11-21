package sbxlogger

import (
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	sandboxLoggerInternal logger.Logger = logger.NewNopLogger()
	sandboxLoggerExternal logger.Logger = logger.NewNopLogger()
)

func SetSandboxLoggerInternal(logger logger.Logger) {
	sandboxLoggerInternal = logger
}

func SetSandboxLoggerExternal(logger logger.Logger) {
	sandboxLoggerExternal = logger.DisableContextual()
}

func I(m LoggerMetadata) *SandboxLogger {
	return &SandboxLogger{sandboxLoggerInternal.With(m.LoggerMetadata().Fields()...)}
}

func E(m LoggerMetadata) *SandboxLogger {
	return &SandboxLogger{sandboxLoggerExternal.With(m.LoggerMetadata().Fields()...)}
}
