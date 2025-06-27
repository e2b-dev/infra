package filesystem

import (
	"fmt"
	"os"
	"os/user"
	"syscall"

	"path"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"google.golang.org/protobuf/types/known/timestamppb"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
	"golang.org/x/mod/semver"
	"google.golang.org/protobuf/types/known/timestamppb"

	rpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/filesystem"
)

const entryInfoSupportedSDKVersion = "1.6.0"

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

func IsGTEVersion(curVersion, minVersion string) bool {
	if len(curVersion) > 0 && curVersion[0] != 'v' {
		curVersion = "v" + curVersion
	}

	if !semver.IsValid(curVersion) {
		return false
	}

	return semver.Compare(curVersion, minVersion) >= 0
}

func entryInfoFromFileInfo(fileInfo os.FileInfo, path string, sdkLanguage string, sdkVersion string) *rpc.EntryInfo {
	owner, group := getFileOwnership(fileInfo)
	fileMode := fileInfo.Mode()

	entry := &rpc.EntryInfo{
		Name: fileInfo.Name(),
		Type: getEntryType(fileInfo),
		Path: path,
	}

	shouldIncludeFields := sdkLanguage == "" || (sdkLanguage == "python" && IsGTEVersion(sdkVersion, entryInfoSupportedSDKVersion))

	if shouldIncludeFields {
		entry.Size = fileInfo.Size()
		entry.Mode = uint32(fileMode.Perm())
		entry.Permissions = fileMode.String()
		entry.Owner = owner
		entry.Group = group
		entry.ModifiedTime = timestamppb.New(fileInfo.ModTime())
	}

	return entry
}
