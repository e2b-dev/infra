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
	return p.HeaderFile(MemfileName)
}

func (p Paths) Rootfs() string {
	return fmt.Sprintf("%s/%s", p.BuildID, RootfsName)
}

func (p Paths) RootfsHeader() string {
	return p.HeaderFile(RootfsName)
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

// HeaderFile returns the storage path for a header sidecar of a data file
// (e.g. "memfile" → "{buildID}/memfile.header").
func (p Paths) HeaderFile(name string) string {
	return fmt.Sprintf("%s/%s%s", p.BuildID, name, HeaderSuffix)
}

// SplitPath splits a storage path of the form "{buildID}/{fileName}"
// back into its components. This is the inverse of the path methods.
func SplitPath(path string) (buildID, fileName string) {
	buildID, fileName, _ = strings.Cut(path, "/")

	return buildID, fileName
}

var knownCompressionSuffixes = []string{CompressionLZ4.Suffix(), CompressionZstd.Suffix()}

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

// SizeSidecar returns the sidecar path that stores the original
// uncompressed size for a compressed object (e.g. "/data/memfile.zstd" →
// "/data/memfile.zstd.uncompressed-size"). Used by the FS backend where
// GCS-style object metadata is unavailable.
func SizeSidecar(objectPath string) string {
	return objectPath + "." + ObjectMetadataUncompressedSize
}

// seekableObjectType derives the metric file_type and codec from a data-file
// path (e.g. "{buildID}/memfile.zstd"), so they need not be threaded through the
// read path.
func seekableObjectType(path string) (SeekableObjectType, CompressionType) {
	_, name := SplitPath(path)
	ct := compressionType(name)

	switch StripCompression(name) {
	case MemfileName:
		return MemfileObjectType, ct
	case RootfsName:
		return RootFSObjectType, ct
	default:
		return UnknownSeekableObjectType, ct
	}
}

// Blob file_type values. The read.blob* metrics cover whole-object (WriteTo)
// reads only — a small, fixed set. The seekable data files (memfile,
// rootfs.ext4) are NOT blobs: they are range-read and recorded under read.read
// with their own vocabulary, so they never appear here. Everything that isn't a
// known type collapses to "other": content-addressed cache blobs are keyed by
// hash, and letting those (or any per-build path) into the label is what blew
// file_type up to ~10^5 distinct values.
const (
	blobTypeHeader   = "header"   // memfile/rootfs header sidecar (*.header)
	blobTypeSnapfile = "snapfile" // VM snapshot file
	blobTypeMetadata = "metadata" // metadata.json
	blobTypeOther    = "other"    // anything else — never a raw hash/ID
)

// blobType classifies a whole-object blob read into the fixed file_type set
// above; anything unrecognized collapses to "other" to keep the label bounded.
func blobType(path string) string {
	name := StripCompression(path[strings.LastIndex(path, "/")+1:])

	switch {
	case strings.HasSuffix(name, HeaderSuffix):
		return blobTypeHeader
	case name == SnapfileName:
		return blobTypeSnapfile
	case name == MetadataName:
		return blobTypeMetadata
	default:
		return blobTypeOther
	}
}

func compressionType(name string) CompressionType {
	switch {
	case strings.HasSuffix(name, CompressionLZ4.Suffix()):
		return CompressionLZ4
	case strings.HasSuffix(name, CompressionZstd.Suffix()):
		return CompressionZstd
	default:
		return CompressionNone
	}
}
