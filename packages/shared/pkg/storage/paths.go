package storage

import (
	"fmt"
	"strings"
)

const (
	GuestEnvdPath = "/usr/bin/envd"

	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
	MetadataName = "metadata.json"

	HeaderSuffix = ".header"
)

type Paths struct {
	BuildID string `json:"build_id"`
}

// Key for the cache. Unique for template-build pair.
func (p Paths) CacheKey() string {
	return p.BuildID
}

func (p Paths) StorageDir() string {
	return p.BuildID
}

func (p Paths) Memfile() string {
	return fmt.Sprintf("%s/%s", p.BuildID, MemfileName)
}

func (p Paths) MemfileHeader() string {
	return fmt.Sprintf("%s/%s%s", p.BuildID, MemfileName, HeaderSuffix)
}

func (p Paths) Rootfs() string {
	return fmt.Sprintf("%s/%s", p.BuildID, RootfsName)
}

func (p Paths) RootfsHeader() string {
	return fmt.Sprintf("%s/%s%s", p.BuildID, RootfsName, HeaderSuffix)
}

func (p Paths) Snapfile() string {
	return fmt.Sprintf("%s/%s", p.BuildID, SnapfileName)
}

func (p Paths) Metadata() string {
	return fmt.Sprintf("%s/%s", p.BuildID, MetadataName)
}

func (p Paths) MemfileCompressed(ct CompressionType) string {
	return fmt.Sprintf("%s/%s%s", p.BuildID, MemfileName, ct.Suffix())
}

func (p Paths) RootfsCompressed(ct CompressionType) string {
	return fmt.Sprintf("%s/%s%s", p.BuildID, RootfsName, ct.Suffix())
}

// DataFile returns the storage path for a data file (e.g. "memfile", "rootfs.ext4"),
// with compression suffix appended if ct is not CompressionNone.
func (p Paths) DataFile(name string, ct CompressionType) string {
	if ct == CompressionNone {
		return fmt.Sprintf("%s/%s", p.BuildID, name)
	}

	return fmt.Sprintf("%s/%s%s", p.BuildID, name, ct.Suffix())
}

// SplitPath splits a storage path of the form "{buildID}/{fileName}"
// back into its components. This is the inverse of the path methods.
func SplitPath(path string) (buildID, fileName string) {
	buildID, fileName, _ = strings.Cut(path, "/")

	return buildID, fileName
}

// StripCompression removes a known compression suffix from a file name.
// For example: "memfile.zstd" → "memfile".
// If no known suffix is present, the name is returned unchanged.
func StripCompression(name string) string {
	for _, suffix := range knownCompressionSuffixes {
		if before, ok := strings.CutSuffix(name, suffix); ok {
			return before
		}
	}

	return name
}

// AppendCompression adds a compression suffix to a path.
// For example: "buildId/memfile" → "buildId/memfile.zstd".
func AppendCompression(path string, ct CompressionType) string {
	return path + ct.Suffix()
}

var knownCompressionSuffixes = []string{".lz4", ".zstd"}
