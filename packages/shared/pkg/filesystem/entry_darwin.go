//go:build darwin

package filesystem

import "syscall"

func extractStatTimes(base *syscall.Stat_t) statTimes {
	return statTimes{
		atime: toTimestamp(base.Atimespec),
		ctime: toTimestamp(base.Birthtimespec),
		mtime: toTimestamp(base.Mtimespec),
		uid:   base.Uid,
		gid:   base.Gid,
	}
}

func readEntryMetadata(_ string) map[string]string {
	return nil
}

// ReadMetadata is a no-op on darwin; envd only reads xattrs on Linux.
func ReadMetadata(_ string) (map[string]string, error) {
	return nil, nil
}

// WriteMetadata is a no-op on darwin; envd only writes xattrs on Linux.
func WriteMetadata(_ string, _ map[string]string) error {
	return nil
}
