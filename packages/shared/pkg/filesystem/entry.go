package filesystem

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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

	if !fillFromBase(fileInfo.Sys(), &entry) {
		// don't know what the base is, fill what little we can
		entry.ModifiedTime = fileInfo.ModTime()
	}

	return entry
}

func fillFromBase(sys any, entry *EntryInfo) bool {
	if sys == nil {
		return false
	}

	st, ok := sys.(*syscall.Stat_t)
	if ok {
		if st == nil {
			return false
		}
		entry.AccessedTime = toTimestampFromSyscall(st.Atim)
		entry.CreatedTime = toTimestampFromSyscall(st.Ctim)
		entry.ModifiedTime = toTimestampFromSyscall(st.Mtim)
		entry.UID = st.Uid
		entry.GID = st.Gid

		return true
	}

	uxt, ok := sys.(*unix.Stat_t)
	if ok {
		if uxt == nil {
			return false
		}
		entry.AccessedTime = toTimestampFromUnix(uxt.Atim)
		entry.CreatedTime = toTimestampFromUnix(uxt.Ctim)
		entry.ModifiedTime = toTimestampFromUnix(uxt.Mtim)
		entry.UID = uxt.Uid
		entry.GID = uxt.Gid

		return true
	}

	return false
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

func toTimestampFromSyscall(spec syscall.Timespec) time.Time {
	if spec.Sec == 0 && spec.Nsec == 0 {
		return time.Time{}
	}

	return time.Unix(spec.Sec, spec.Nsec)
}

func toTimestampFromUnix(spec unix.Timespec) time.Time {
	if spec.Sec == 0 && spec.Nsec == 0 {
		return time.Time{}
	}

	return time.Unix(spec.Sec, spec.Nsec)
}
