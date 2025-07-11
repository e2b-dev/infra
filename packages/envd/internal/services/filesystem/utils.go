package filesystem

import (
	"path"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

type osEntry interface {
	IsDir() bool
}

// getFolder returns the path of the directory the entry belongs to.
func getFolder(entry *rpc.EntryInfo) string {
	if entry.Type == rpc.FileType_FILE_TYPE_DIRECTORY {
		return entry.Path
	}

	return path.Dir(entry.Path)
}

func getEntryType(entry osEntry) rpc.FileType {
	if entry.IsDir() {
		return rpc.FileType_FILE_TYPE_DIRECTORY
	} else {
		return rpc.FileType_FILE_TYPE_FILE
	}
}
