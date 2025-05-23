package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"google.golang.org/protobuf/types/known/timestamppb"
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

func EntryInfoFromFileInfo(fileInfo os.FileInfo, path string) *rpc.EntryInfo {
	owner, group := getFileOwnership(fileInfo)
	fileMode := fileInfo.Mode()

	return &rpc.EntryInfo{
		Name:         fileInfo.Name(),
		Type:         getEntryType(fileInfo),
		Path:         path,
		Size:         fileInfo.Size(),
		Mode:         uint32(fileMode.Perm()),
		Permissions:  fileMode.String(),
		Owner:        owner,
		Group:        group,
		ModifiedTime: timestamppb.New(fileInfo.ModTime()),
	}
}
