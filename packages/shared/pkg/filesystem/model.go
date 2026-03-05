package filesystem

import (
	"os"
	"time"
)

type EntryInfo struct {
	Name          string
	Type          FileType
	Path          string
	Size          int64
	Mode          os.FileMode
	Permissions   string
	UID           uint32
	GID           uint32
	AccessedTime  time.Time
	CreatedTime   time.Time
	ModifiedTime  time.Time
	SymlinkTarget *string
}

type FileType int32

const (
	UnknownFileType   FileType = 0
	FileFileType      FileType = 1
	DirectoryFileType FileType = 2
	SymlinkFileType   FileType = 3
)
