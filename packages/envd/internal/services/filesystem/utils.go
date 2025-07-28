package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

// getEntryType determines the type of file entry based on its mode and path.
// If the file is a symlink, it follows the symlink to determine the actual type.
func getEntryType(mode os.FileMode) rpc.FileType {
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
	owner = fmt.Sprintf("%d", sys.Uid)
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}

	// Look up group name
	group = fmt.Sprintf("%d", sys.Gid)
	if g, err := user.LookupGroupId(fmt.Sprintf("%d", sys.Gid)); err == nil {
		group = g.Name
	}

	return owner, group
}

func entryInfo(path string) (*rpc.EntryInfo, error) {
	// Get file info using Lstat to handle symlinks correctly
	fileInfo, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("file not found: %w", err))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error getting file info: %w", err))
	}

	owner, group := getFileOwnership(fileInfo)
	fileMode := fileInfo.Mode()

	var symlinkTarget *string
	if fileMode&os.ModeSymlink != 0 {
		// If we can't resolve the symlink target, we won't set the target
		target, err := followSymlink(path)
		if err == nil {
			symlinkTarget = &target
		}
	}

	var entryType rpc.FileType
	var mode uint32

	if symlinkTarget == nil {
		entryType = getEntryType(fileMode)
		mode = uint32(fileMode.Perm())
	} else {
		// If it's a symlink, we need to determine the type of the target
		targetInfo, err := os.Stat(*symlinkTarget)
		if err != nil {
			entryType = rpc.FileType_FILE_TYPE_UNSPECIFIED
		} else {
			entryType = getEntryType(targetInfo.Mode())
			mode = uint32(targetInfo.Mode().Perm())
		}
	}

	return &rpc.EntryInfo{
		Name:          fileInfo.Name(),
		Type:          entryType,
		Path:          path,
		Size:          fileInfo.Size(),
		Mode:          mode,
		Permissions:   fileMode.String(),
		Owner:         owner,
		Group:         group,
		ModifiedTime:  timestamppb.New(fileInfo.ModTime()),
		SymlinkTarget: symlinkTarget,
	}, nil
}
