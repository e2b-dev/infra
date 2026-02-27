package storage

import (
	"fmt"
)

const (
	GuestEnvdPath = "/usr/bin/envd"

	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
	MetadataName = "metadata.json"

	HeaderSuffix = ".header"

	// v4Prefix is prepended to the base filename for all v4 compressed assets.
	v4Prefix = "v4."

	// v4HeaderSuffix is the suffix after the base filename for v4 headers.
	// V4 headers are always LZ4-block-compressed.
	v4HeaderSuffix = ".header.lz4"
)

type TemplateFiles struct {
	BuildID string `json:"build_id"`
}

// Key for the cache. Unique for template-build pair.
func (t TemplateFiles) CacheKey() string {
	return t.BuildID
}

func (t TemplateFiles) StorageDir() string {
	return t.BuildID
}

func (t TemplateFiles) StorageMemfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MemfileName)
}

func (t TemplateFiles) StorageMemfileHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), MemfileName, HeaderSuffix)
}

func (t TemplateFiles) StorageRootfsPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), RootfsName)
}

func (t TemplateFiles) StorageRootfsHeaderPath() string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), RootfsName, HeaderSuffix)
}

func (t TemplateFiles) StorageSnapfilePath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), SnapfileName)
}

func (t TemplateFiles) StorageMetadataPath() string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), MetadataName)
}

// HeaderPath returns the header storage path for a given file name within this build.
func (t TemplateFiles) HeaderPath(fileName string) string {
	return fmt.Sprintf("%s/%s%s", t.StorageDir(), fileName, HeaderSuffix)
}

// V4DataName returns the v4 data filename: "v4.memfile.lz4".
func V4DataName(fileName string, ct CompressionType) string {
	return v4Prefix + fileName + ct.Suffix()
}

// V4HeaderName returns the v4 header filename: "v4.memfile.header.lz4".
func V4HeaderName(fileName string) string {
	return v4Prefix + fileName + v4HeaderSuffix
}

// V4DataPath transforms a base object path (e.g. "buildId/memfile") into
// the v4 compressed data path (e.g. "buildId/v4.memfile.lz4").
func V4DataPath(basePath string, ct CompressionType) string {
	dir, file := splitPath(basePath)

	return dir + V4DataName(file, ct)
}

// splitPath splits "dir/file" into ("dir/", "file"). If there's no slash,
// dir is empty.
func splitPath(p string) (dir, file string) {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i+1], p[i+1:]
		}
	}

	return "", p
}

// CompressedDataPath returns the v4 compressed data path for a given file name.
// Example: "{buildId}/v4.memfile.lz4"
func (t TemplateFiles) CompressedDataPath(fileName string, ct CompressionType) string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), V4DataName(fileName, ct))
}

// CompressedHeaderPath returns the v4 header path: "{buildId}/v4.{fileName}.header.lz4".
func (t TemplateFiles) CompressedHeaderPath(fileName string) string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), V4HeaderName(fileName))
}
