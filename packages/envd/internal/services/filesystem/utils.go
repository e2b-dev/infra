package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/filesystem"
)

// Filesystem magic numbers from Linux kernel (include/uapi/linux/magic.h)
const (
	nfsSuperMagic   = 0x6969
	cifsMagic       = 0xFF534D42
	smbSuperMagic   = 0x517B
	smb2MagicNumber = 0xFE534D42
	fuseSuperMagic  = 0x65735546
)

// IsPathOnNetworkMount checks if the given path is on a network filesystem mount.
// Returns true if the path is on NFS, CIFS, SMB, or FUSE filesystem.
func IsPathOnNetworkMount(path string) (bool, error) {
	var statfs syscall.Statfs_t
	if err := syscall.Statfs(path, &statfs); err != nil {
		return false, fmt.Errorf("failed to statfs %s: %w", path, err)
	}

	switch statfs.Type {
	case nfsSuperMagic, cifsMagic, smbSuperMagic, smb2MagicNumber, fuseSuperMagic:
		return true, nil
	default:
		return false, nil
	}
}

func entryInfo(path string) (*rpc.EntryInfo, error) {
	info, err := filesystem.GetEntryFromPath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("file not found: %w", err))
		}

		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("error getting file info: %w", err))
	}

	owner, group := getFileOwnership(info)

	return &rpc.EntryInfo{
		Name:          info.Name,
		Type:          getEntryType(info.Type),
		Path:          info.Path,
		Size:          info.Size,
		Mode:          uint32(info.Mode),
		Permissions:   info.Permissions,
		Owner:         owner,
		Group:         group,
		ModifiedTime:  toTimestamp(info.ModifiedTime),
		SymlinkTarget: info.SymlinkTarget,
	}, nil
}

func toTimestamp(time time.Time) *timestamppb.Timestamp {
	if time.IsZero() {
		return nil
	}

	return timestamppb.New(time)
}

// getFileOwnership returns the owner and group names for a file.
// If the lookup fails, it returns the numeric UID and GID as strings.
func getFileOwnership(fileInfo filesystem.EntryInfo) (owner, group string) {
	// Look up username
	owner = fmt.Sprintf("%d", fileInfo.UID)
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}

	// Look up group name
	group = fmt.Sprintf("%d", fileInfo.GID)
	if g, err := user.LookupGroupId(group); err == nil {
		group = g.Name
	}

	return owner, group
}

// getEntryType determines the type of file entry based on its mode and path.
// If the file is a symlink, it follows the symlink to determine the actual type.
func getEntryType(fileType filesystem.FileType) rpc.FileType {
	switch fileType {
	case filesystem.FileFileType:
		return rpc.FileType_FILE_TYPE_FILE
	case filesystem.DirectoryFileType:
		return rpc.FileType_FILE_TYPE_DIRECTORY
	case filesystem.SymlinkFileType:
		return rpc.FileType_FILE_TYPE_SYMLINK
	default:
		return rpc.FileType_FILE_TYPE_UNSPECIFIED
	}
}
