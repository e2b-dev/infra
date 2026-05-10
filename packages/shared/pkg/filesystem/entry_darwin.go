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
