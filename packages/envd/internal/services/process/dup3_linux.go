//go:build linux

package process

import "golang.org/x/sys/unix"

// dup3 duplicates oldfd onto newfd via dup3(2) with the given flags. Linux-only;
// the live upgrade uses it (with flags 0) to relocate carried fds with CLOEXEC
// cleared so they survive execve.
func dup3(oldfd, newfd, flags int) error {
	return unix.Dup3(oldfd, newfd, flags)
}
