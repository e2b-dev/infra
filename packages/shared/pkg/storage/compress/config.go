// Package compress provides zstd seekable compression for storage objects.
// Data is split into independently-compressed frames with a seek table for
// random-access reads. Compression is parallelized across CPU cores.
package compress

import "fmt"

const (
	// FrameSize is the uncompressed data per frame. Aligned to the hugepage
	// size so each frame maps 1:1 to a memory page during resume.
	FrameSize = 2 * 1024 * 1024 // 2 MB

	// MaxFrameData is the upper bound on uncompressed bytes in any frame.
	// Compressed output will be smaller (typically 4-5x for VM memory).
	MaxFrameData = FrameSize
)

// Config holds compression parameters. A nil *Config means no compression.
type Config struct {
	Level       int
	Concurrency int
}

func DefaultConfig() *Config {
	return &Config{Level: 1}
}

func (c *Config) level() int {
	if c.Level > 0 {
		return c.Level
	}
	return 1
}

func (c *Config) concurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return 0
}

func (c *Config) String() string {
	if c == nil {
		return "none"
	}
	return fmt.Sprintf("zstd (level=%d, frame=%dMB)", c.level(), FrameSize/(1024*1024))
}
