package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"

	"google.golang.org/protobuf/types/known/timestamppb"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

// getEntryType determines the type of file entry based on its mode and path.
// If the file is a symlink, it follows the symlink to determine the actual type.
func getEntryType(mode os.FileMode, path string) rpc.FileType {
	if mode&os.ModeSymlink != 0 {
		targetPath, err := followSymlink(path)
		if err != nil {
			return rpc.FileType_FILE_TYPE_UNSPECIFIED
		}

		info, err := os.Lstat(targetPath)
		if err != nil {
			return rpc.FileType_FILE_TYPE_UNSPECIFIED
		}

		mode = info.Mode()
	}

	switch {
	case mode.IsRegular():
		return rpc.FileType_FILE_TYPE_FILE
	case mode.IsDir():
		return rpc.FileType_FILE_TYPE_DIRECTORY
	default:
		return rpc.FileType_FILE_TYPE_UNSPECIFIED
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

func entryInfoFromFileInfo(fileInfo os.FileInfo, path string) *rpc.EntryInfo {
	owner, group := getFileOwnership(fileInfo)
	fileMode := fileInfo.Mode()

	var symlinkTarget string
	if fileMode&os.ModeSymlink != 0 {
		// If we can't resolve the symlink target, we won't set the target
		symlinkTarget, _ = followSymlink(path)
	}

	return &rpc.EntryInfo{
		Name:          fileInfo.Name(),
		Type:          getEntryType(fileMode, path),
		Path:          path,
		Size:          fileInfo.Size(),
		Mode:          uint32(fileMode.Perm()),
		Permissions:   fileMode.String(),
		Owner:         owner,
		Group:         group,
		ModifiedTime:  timestamppb.New(fileInfo.ModTime()),
		SymlinkTarget: symlinkTarget,
	}
}
