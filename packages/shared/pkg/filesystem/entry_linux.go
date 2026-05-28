//go:build linux

package filesystem

import "syscall"

func extractStatTimes(base *syscall.Stat_t) statTimes {
	return statTimes{
		atime: toTimestamp(base.Atim),
		ctime: toTimestamp(base.Ctim),
		mtime: toTimestamp(base.Mtim),
		uid:   base.Uid,
		gid:   base.Gid,
	}
}
