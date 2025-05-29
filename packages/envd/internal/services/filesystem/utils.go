package filesystem

import (
	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

type osEntry interface {
	IsDir() bool
}

func getEntryType(entry osEntry) rpc.FileType {
	if entry.IsDir() {
		return rpc.FileType_FILE_TYPE_DIRECTORY
	} else {
		return rpc.FileType_FILE_TYPE_FILE
	}
}
