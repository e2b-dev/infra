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

// SuppressNoisyLogs disables verbose output from OTEL tracing, LaunchDarkly, and standard log.
// Only ERROR level and above will be logged.
func SuppressNoisyLogs() {
	// Silence standard log package
	log.SetOutput(io.Discard)
	// Replace global zap logger with error-only logger
	setErrorOnlyLogger()
}

// SuppressNoisyLogsKeepStdLog disables verbose output but keeps standard log enabled.
func SuppressNoisyLogsKeepStdLog() {
	setErrorOnlyLogger()
}

// setErrorOnlyLogger replaces the global zap logger with one that only logs errors.
func setErrorOnlyLogger() {
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	errLogger, err := cfg.Build()
	if err == nil {
		zap.ReplaceGlobals(errLogger)
	}
}

// GetHeaderInfo reads a header file and returns total size and block size.
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

// GetFileSizes returns the logical size and actual on-disk size of a file.
func GetFileSizes(path string) (logical, actual int64, err error) {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return 0, 0, err
	}

	return stat.Size, stat.Blocks * 512, nil
}

// GetActualFileSize returns only the actual on-disk size of a file.
func GetActualFileSize(path string) (int64, error) {
	_, actual, err := GetFileSizes(path)

	return actual, err
}

// ArtifactInfo contains information about a build artifact.
type ArtifactInfo struct {
	Name            string
	File            string   // e.g., "memfile"
	HeaderFile      string   // e.g., "memfile.header"
	CompressedFiles []string // e.g., ["memfile.lz4", "memfile.zstd"]
}

// allCompressionTypes lists all supported compression types for file probing.
var allCompressionTypes = []storage.CompressionType{
	storage.CompressionLZ4,
	storage.CompressionZstd,
}

// MainArtifacts returns the list of main artifacts (rootfs, memfile).
func MainArtifacts() []ArtifactInfo {
	return []ArtifactInfo{
		{
			Name:            "Rootfs",
			File:            storage.RootfsName,
			HeaderFile:      storage.RootfsName + storage.HeaderSuffix,
			CompressedFiles: compressedDataNames(storage.RootfsName),
		},
		{
			Name:            "Memfile",
			File:            storage.MemfileName,
			HeaderFile:      storage.MemfileName + storage.HeaderSuffix,
			CompressedFiles: compressedDataNames(storage.MemfileName),
		},
	}
}

func compressedDataNames(fileName string) []string {
	names := make([]string, len(allCompressionTypes))
	for i, ct := range allCompressionTypes {
		names[i] = storage.CompressedDataName(fileName, ct)
	}

	return names
}

// SmallArtifacts returns the list of small artifacts (headers, snapfile, metadata).
func SmallArtifacts() []struct{ Name, File string } {
	return []struct{ Name, File string }{
		{"Rootfs header", storage.RootfsName + storage.HeaderSuffix},
		{"Memfile header", storage.MemfileName + storage.HeaderSuffix},
		{"Snapfile", storage.SnapfileName},
		{"Metadata", storage.MetadataName},
	}
}
