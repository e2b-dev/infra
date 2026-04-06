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

// SplitPath splits a storage path of the form "{buildID}/{fileName}"
// back into its components. This is the inverse of the path methods.
func SplitPath(path string) (buildID, fileName string) {
	buildID, fileName, _ = strings.Cut(path, "/")

	return buildID, fileName
}
