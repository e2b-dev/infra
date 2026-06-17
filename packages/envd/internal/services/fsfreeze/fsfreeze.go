// Package fsfreeze freezes and thaws a mounted filesystem via the FIFREEZE /
// FITHAW ioctls. The orchestrator uses it to quiesce the guest rootfs to a
// consistent, fully-flushed on-disk state before a filesystem-only pause,
// closing the sync->pause race where a write acknowledged after sync but before
// pause would otherwise be lost on the reboot resume.
package fsfreeze

// Freezer freezes and thaws the filesystem mounted at a given path.
type Freezer interface {
	// Freeze flushes and suspends all writes to the filesystem containing
	// mountpoint until Thaw is called. Freezing an already-frozen filesystem is
	// a no-op (no error).
	Freeze(mountpoint string) error

	// Thaw resumes writes to the filesystem containing mountpoint. Thawing a
	// filesystem that is not frozen is a no-op (no error).
	Thaw(mountpoint string) error
}
