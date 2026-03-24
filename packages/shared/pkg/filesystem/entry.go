package filesystem

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func GetEntryFromPath(path string) (EntryInfo, error) {
	fileInfo, err := os.Lstat(path)
	if err != nil {
		return EntryInfo{}, err
	}

	return GetEntryInfo(path, fileInfo), nil
}

func GetEntryInfo(path string, fileInfo os.FileInfo) EntryInfo {
	fileMode := fileInfo.Mode()

	var symlinkTarget *string
	if fileMode&os.ModeSymlink != 0 {
		// If we can't resolve the symlink target, we won't set the target
		target := followSymlink(path)
		symlinkTarget = &target
	}

	var entryType FileType
	var mode os.FileMode

	if symlinkTarget == nil {
		entryType = getEntryType(fileMode)
		mode = fileMode.Perm()
	} else {
		// If it's a symlink, we need to determine the type of the target
		targetInfo, err := os.Stat(*symlinkTarget)
		if err != nil {
			entryType = UnknownFileType
		} else {
			entryType = getEntryType(targetInfo.Mode())
			mode = targetInfo.Mode().Perm()
		}
	}

	entry := EntryInfo{
		Name:          fileInfo.Name(),
		Path:          path,
		Type:          entryType,
		Size:          fileInfo.Size(),
		Mode:          mode,
		Permissions:   fileMode.String(),
		ModifiedTime:  fileInfo.ModTime(),
		SymlinkTarget: symlinkTarget,
	}

	if base := getBase(fileInfo.Sys()); base != nil {
		entry.AccessedTime = toTimestamp(base.Atim)
		entry.CreatedTime = toTimestamp(base.Ctim)
		entry.ModifiedTime = toTimestamp(base.Mtim)
		entry.UID = base.Uid
		entry.GID = base.Gid
	} else if !fileInfo.ModTime().IsZero() {
		entry.ModifiedTime = fileInfo.ModTime()
	}

	return entry
}

// getEntryType determines the type of file entry based on its mode and path.
// If the file is a symlink, it follows the symlink to determine the actual type.
func getEntryType(mode os.FileMode) FileType {
	switch {
	case mode.IsRegular():
		return FileFileType
	case mode.IsDir():
		return DirectoryFileType
	case mode&os.ModeSymlink == os.ModeSymlink:
		return SymlinkFileType
	default:
		return UnknownFileType
	}
}

// followSymlink resolves a symbolic link to its target path.
func followSymlink(path string) string {
	// Resolve symlinks
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}

	return resolvedPath
}

func toTimestamp(spec syscall.Timespec) time.Time {
	if spec.Sec == 0 && spec.Nsec == 0 {
		return time.Time{}
	}

	return time.Unix(spec.Sec, spec.Nsec)
}

func getBase(sys any) *syscall.Stat_t {
	st, _ := sys.(*syscall.Stat_t)

	return st
}
