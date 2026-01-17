package slogged

import (
	"os"
	"time"

	"github.com/go-git/go-billy/v5"
)

type sloggedChange struct {
	inner billy.Change
}

var _ billy.Change = (*sloggedChange)(nil)

func newChange(change billy.Change) sloggedChange {
	return sloggedChange{change}
}

func (s sloggedChange) Chmod(name string, mode os.FileMode) (err error) {
	slogStart("Chmod")
	defer func() { slogEndWithError("Chmod", err) }()

	return s.inner.Chmod(name, mode)
}

func (s sloggedChange) Lchown(name string, uid, gid int) (err error) {
	slogStart("Lchown")
	defer func() { slogEndWithError("Lchown", err) }()

	return s.inner.Lchown(name, uid, gid)
}

func (s sloggedChange) Chown(name string, uid, gid int) (err error) {
	slogStart("Chown")
	defer func() { slogEndWithError("Chown", err) }()

	return s.inner.Chown(name, uid, gid)
}

func (s sloggedChange) Chtimes(name string, atime time.Time, mtime time.Time) (err error) {
	slogStart("Chtimes")
	defer func() { slogEndWithError("Chtimes", err) }()

	return s.inner.Chtimes(name, atime, mtime)
}
