package storage

import (
	"context"
	"fmt"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

const (
	// DefaultCompressFrameSize is the default uncompressed size of each
	// compression frame (2 MiB). Overridable via CompressConfig.FrameSizeKB.
	// The last frame in a file may be shorter.
	//
	// The chunker fetches one frame at a time from storage on a cache miss.
	// Larger frame sizes mean more data cached per fetch (faster warm-up and
	// fewer remote storage round-trips), but higher memory and I/O cost per
	// miss.
	//
	// This MUST be multiple of every block/page size:
	//   - header.HugepageSize (2 MiB) — UFFD huge-page size, also used by prefetch
	//   - header.RootfsBlockSize (4 KiB) — NBD / rootfs block size
	DefaultCompressFrameSize = 2 * 1024 * 1024

	// Use case identifiers for per-use-case compression targeting via LaunchDarkly.
	UseCaseBuild = "build"
	UseCasePause = "pause"
)

// CompressConfig is the base compression configuration, loaded from environment
// variables at startup. Feature flags can override individual fields at runtime
// via ResolveCompressConfig. Zero value means compression disabled.
type CompressConfig struct {
	Enabled            bool   `env:"COMPRESS_ENABLED"              envDefault:"false"`
	Type               string `env:"COMPRESS_TYPE"                 envDefault:""`
	Level              int    `env:"COMPRESS_LEVEL"                envDefault:"0"`
	FrameSizeKB        int    `env:"COMPRESS_FRAME_SIZE_KB"        envDefault:"0"`
	MinPartSizeMB      int    `env:"COMPRESS_MIN_PART_SIZE_MB"     envDefault:"0"`
	FrameEncodeWorkers int    `env:"COMPRESS_FRAME_ENCODE_WORKERS" envDefault:"0"`
	EncoderConcurrency int    `env:"COMPRESS_ENCODER_CONCURRENCY"  envDefault:"0"`
}

// CompressionType returns the parsed CompressionType.
func (c CompressConfig) CompressionType() CompressionType {
	return parseCompressionType(c.Type)
}

// FrameSize returns the frame size in bytes.
func (c CompressConfig) FrameSize() int {
	if c.FrameSizeKB <= 0 {
		return DefaultCompressFrameSize
	}

	return c.FrameSizeKB * 1024
}

// MinPartSize returns the minimum compressed part size in bytes.
// Parts accumulate frames until they reach this threshold.
func (c CompressConfig) MinPartSize() int64 {
	if c.MinPartSizeMB <= 0 {
		return int64(gcpMultipartUploadChunkSize)
	}

	return int64(c.MinPartSizeMB) * (1 << 20)
}

// IsCompressionEnabled reports whether compression is configured and active.
func (c CompressConfig) IsCompressionEnabled() bool {
	return c.Enabled && c.CompressionType() != CompressionNone
}

// Validate checks that the config is internally consistent.
func (c CompressConfig) Validate() error {
	if !c.IsCompressionEnabled() {
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

// ResolveCompressConfig returns the effective compression config for a given
// file type and use case. Feature flags override the base config when active.
// Returns zero-value CompressConfig when compression is disabled.
//
// fileType and useCase are added to the LD evaluation context so that
// LaunchDarkly targeting rules can differentiate (e.g. compress memfile
// but not rootfs, or compress builds but not pauses).
func ResolveCompressConfig(ctx context.Context, base CompressConfig, ff *featureflags.Client, fileType, useCase string) CompressConfig {
	if ff != nil {
		var extra []ldcontext.Context
		if fileType != "" {
			extra = append(extra, featureflags.CompressFileTypeContext(fileType))
		}
		if useCase != "" {
			extra = append(extra, featureflags.CompressUseCaseContext(useCase))
		}
		ctx = featureflags.AddToContext(ctx, extra...)

		v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()

		if v.Get("compressBuilds").BoolValue() {
			ct := v.Get("compressionType").StringValue()
			if parseCompressionType(ct) != CompressionNone {
				return CompressConfig{
					Enabled:            true,
					Type:               ct,
					Level:              v.Get("compressionLevel").IntValue(),
					FrameSizeKB:        v.Get("frameSizeKB").IntValue(),
					MinPartSizeMB:      v.Get("minPartSizeMB").IntValue(),
					FrameEncodeWorkers: v.Get("frameEncodeWorkers").IntValue(),
					EncoderConcurrency: v.Get("encoderConcurrency").IntValue(),
				}
			}
		}
	}

	if !base.IsCompressionEnabled() {
		return CompressConfig{}
	}

	return base
}
