package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"

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

// getFileOwnership returns the owner and group names for a file.
// If the lookup fails, it returns the numeric UID and GID as strings.
func getFileOwnership(fileInfo os.FileInfo) (owner, group string) {
	sys, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}

	// Look up username
	if u, err := user.LookupId(fmt.Sprintf("%d", sys.Uid)); err == nil {
		owner = u.Username
	} else {
		owner = fmt.Sprintf("%d", sys.Uid)
	}

	// Look up group name
	if g, err := user.LookupGroupId(fmt.Sprintf("%d", sys.Gid)); err == nil {
		group = g.Name
	} else {
		group = fmt.Sprintf("%d", sys.Gid)
	}

	return owner, group
}
