// Package cmdutil provides shared utilities for CLI commands.
package cmdutil

import (
	"io"
	"log"
	"os"
	"syscall"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func SuppressNoisyLogs() {
	log.SetOutput(io.Discard)
	setErrorOnlyLogger()
}

func SuppressNoisyLogsKeepStdLog() {
	setErrorOnlyLogger()
}

func setErrorOnlyLogger() {
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	errLogger, err := cfg.Build()
	if err == nil {
		zap.ReplaceGlobals(errLogger)
	}
}

func GetHeaderInfo(headerPath string) (totalSize, blockSize uint64) {
	data, err := os.ReadFile(headerPath)
	if err != nil {
		return 0, 0
	}
	h, err := header.Deserialize(data)
	if err != nil {
		return 0, 0
	}

	return h.Metadata.Size, h.Metadata.BlockSize
}

func GetFileSizes(path string) (logical, actual int64, err error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return 0, 0, err
	}

	return stat.Size, stat.Blocks * 512, nil
}

func GetActualFileSize(path string) (int64, error) {
	_, actual, err := GetFileSizes(path)

	return actual, err
}

type ArtifactInfo struct {
	Name       string
	File       string
	HeaderFile string
}

func MainArtifacts() []ArtifactInfo {
	return []ArtifactInfo{
		{"Rootfs", storage.RootfsName, storage.RootfsName + storage.HeaderSuffix},
		{"Memfile", storage.MemfileName, storage.MemfileName + storage.HeaderSuffix},
	}
}

func SmallArtifacts() []struct{ Name, File string } {
	return []struct{ Name, File string }{
		{"Rootfs header", storage.RootfsName + storage.HeaderSuffix},
		{"Memfile header", storage.MemfileName + storage.HeaderSuffix},
		{"Snapfile", storage.SnapfileName},
		{"Metadata", storage.MetadataName},
	}
}
