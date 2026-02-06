package jailed

import (
	"os"
	"strings"

	"github.com/go-git/go-billy/v5"
)

type mountFailedFS struct{}

func (m mountFailedFS) String() string {
	return "mountFailedFS{}"
}

func (m mountFailedFS) Create(_ string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Open(_ string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) OpenFile(_ string, _ int, _ os.FileMode) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Stat(_ string) (os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Rename(_, _ string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Remove(_ string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Join(elem ...string) string {
	return strings.Join(elem, "/")
}

func (m mountFailedFS) TempFile(_, _ string) (billy.File, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) ReadDir(_ string) ([]os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) MkdirAll(_ string, _ os.FileMode) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Lstat(_ string) (os.FileInfo, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Symlink(_, _ string) error {
	return ErrInvalidSandbox
}

func (m mountFailedFS) Readlink(_ string) (string, error) {
	return "", ErrInvalidSandbox
}

func (m mountFailedFS) Chroot(_ string) (billy.Filesystem, error) {
	return nil, ErrInvalidSandbox
}

func (m mountFailedFS) Root() string {
	return ""
}
