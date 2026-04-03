package storage

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// CompressConfig is the base compression configuration, loaded from environment
// variables at startup. Feature flags can override individual fields at runtime
// via ResolveCompressConfig.
type CompressConfig struct {
	Enabled            bool   `env:"COMPRESS_ENABLED"              envDefault:"false"`
	Type               string `env:"COMPRESS_TYPE"                 envDefault:"zstd"`
	Level              int    `env:"COMPRESS_LEVEL"                envDefault:"2"`
	FrameSizeKB        int    `env:"COMPRESS_FRAME_SIZE_KB"        envDefault:"2048"`
	TargetPartSizeMB   int    `env:"COMPRESS_TARGET_PART_SIZE_MB"  envDefault:"50"`
	FrameEncodeWorkers int    `env:"COMPRESS_FRAME_ENCODE_WORKERS" envDefault:"4"`
	EncoderConcurrency int    `env:"COMPRESS_ENCODER_CONCURRENCY"  envDefault:"1"`
}

// CompressionType returns the parsed CompressionType.
func (c *CompressConfig) CompressionType() CompressionType {
	if c == nil {
		return CompressionNone
	}

	return parseCompressionType(c.Type)
}

// FrameSize returns the frame size in bytes.
func (c *CompressConfig) FrameSize() int {
	if c == nil || c.FrameSizeKB <= 0 {
		return DefaultCompressFrameSize
	}

	return c.FrameSizeKB * 1024
}

// TargetPartSize returns the target part size in bytes.
func (c *CompressConfig) TargetPartSize() int64 {
	if c == nil || c.TargetPartSizeMB <= 0 {
		return int64(gcpMultipartUploadChunkSize)
	}

	return int64(c.TargetPartSizeMB) * (1 << 20)
}

// IsEnabled reports whether compression is configured and active.
func (c *CompressConfig) IsEnabled() bool {
	return c != nil && c.Enabled && c.CompressionType() != CompressionNone
}

// Validate checks that the config is internally consistent.
func (c *CompressConfig) Validate() error {
	if c == nil || !c.IsEnabled() {
		return nil
	}

	fs := c.FrameSize()
	if fs <= 0 {
		return fmt.Errorf("frame size must be positive, got %d KB", c.FrameSizeKB)
	}
	if MemoryChunkSize%fs != 0 && fs%MemoryChunkSize != 0 {
		return fmt.Errorf("frame size (%d) must be a divisor or multiple of MemoryChunkSize (%d)", fs, MemoryChunkSize)
	}

	return nil
}

// Resolve returns a pointer to this config if compression is enabled, or nil.
// Callers use nil to mean "no compression".
func (c *CompressConfig) Resolve() *CompressConfig {
	if c == nil || !c.IsEnabled() {
		return nil
	}

	return c
}

// ResolveCompressConfig returns the effective compression config for a given
// file type and use case. Feature flags override the base config when active.
// Returns nil when compression is disabled.
//
// fileType and useCase are added to the LD evaluation context so that
// LaunchDarkly targeting rules can differentiate (e.g. compress memfile
// but not rootfs, or compress builds but not pauses).
func ResolveCompressConfig(ctx context.Context, base CompressConfig, ff *featureflags.Client, fileType, useCase string) *CompressConfig {
	if ff != nil {
		ctx = featureflags.AddToContext(ctx,
			featureflags.CompressFileTypeContext(fileType),
			featureflags.CompressUseCaseContext(useCase),
		)

		v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()

		if v.Get("compressBuilds").BoolValue() {
			ct := v.Get("compressionType").StringValue()
			if parseCompressionType(ct) != CompressionNone {
				return &CompressConfig{
					Enabled:            true,
					Type:               ct,
					Level:              v.Get("compressionLevel").IntValue(),
					FrameSizeKB:        v.Get("frameSizeKB").IntValue(),
					TargetPartSizeMB:   v.Get("targetPartSizeMB").IntValue(),
					FrameEncodeWorkers: v.Get("frameEncodeWorkers").IntValue(),
					EncoderConcurrency: v.Get("encoderConcurrency").IntValue(),
				}
			}
		}
	}

	return base.Resolve()
}
