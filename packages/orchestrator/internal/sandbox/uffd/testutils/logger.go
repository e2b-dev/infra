package testutils

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type testWriter struct {
	t *testing.T
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	w.t.Log(string(p))

	return len(p), nil
}

func NewTestLogger(t *testing.T) *zap.Logger {
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoderCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	encoderCfg.CallerKey = zapcore.OmitKey
	encoderCfg.ConsoleSeparator = "  "
	encoderCfg.TimeKey = ""
	encoderCfg.MessageKey = "message"
	encoderCfg.LevelKey = "level"
	encoderCfg.NameKey = "logger"
	encoderCfg.StacktraceKey = "stacktrace"
	encoderCfg.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	encoderCfg.EncodeCaller = zapcore.ShortCallerEncoder
	encoderCfg.EncodeDuration = zapcore.StringDurationEncoder

	encoder := zapcore.NewConsoleEncoder(encoderCfg)

	testSyncer := zapcore.AddSync(&testWriter{t})
	core := zapcore.NewCore(encoder, testSyncer, zap.DebugLevel)

	return zap.New(core, zap.AddCaller())
}
