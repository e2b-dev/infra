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

// CompressedDataName returns the compressed data filename: "memfile.zstd".
func CompressedDataName(fileName string, ct CompressionType) string {
	return fileName + ct.Suffix()
}

// CompressedDataPath returns the compressed data path for a given file name.
// Example: "{buildId}/memfile.zstd"
func (t TemplateFiles) CompressedDataPath(fileName string, ct CompressionType) string {
	return fmt.Sprintf("%s/%s", t.StorageDir(), CompressedDataName(fileName, ct))
}

// CompressedPath transforms a base object path (e.g. "buildId/memfile") into
// the compressed data path (e.g. "buildId/memfile.zstd").
func CompressedPath(basePath string, ct CompressionType) string {
	return basePath + ct.Suffix()
}
