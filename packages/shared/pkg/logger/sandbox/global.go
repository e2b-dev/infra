package sbxlogger

import "go.uber.org/zap"

var (
	sandboxLoggerInternal *zap.Logger = zap.NewNop()
	sandboxLoggerExternal *zap.Logger = zap.NewNop()
)

func SetSandboxLoggerInternal(logger *zap.Logger) {
	sandboxLoggerInternal = logger
}

func SetSandboxLoggerExternal(logger *zap.Logger) {
	sandboxLoggerExternal = logger
}

func I(m LoggerMetadata) *SandboxLogger {
	return &SandboxLogger{sandboxLoggerInternal.With(m.LoggerMetadata().Fields()...)}
}

func E(m LoggerMetadata) *SandboxLogger {
	return &SandboxLogger{sandboxLoggerExternal.With(m.LoggerMetadata().Fields()...)}
}
