//go:build linux

package filesystem

import (
	"errors"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ReadMetadata returns user-defined metadata stored in xattrs under the
// MetadataXattrPrefix namespace. Returns nil (not an error) when the
// filesystem does not support xattrs or the file has no metadata set.
func ReadMetadata(path string) (map[string]string, error) {
	names, err := listxattr(path)
	if err != nil {
		if isXattrUnsupported(err) {
			return nil, nil
		}

		return nil, err
	}

	var metadata map[string]string
	for _, name := range names {
		if !strings.HasPrefix(name, MetadataXattrPrefix) {
			continue
		}

		value, err := getxattr(path, name)
		if err != nil {
			if errors.Is(err, syscall.ENODATA) {
				continue
			}

			return nil, err
		}

		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata[strings.TrimPrefix(name, MetadataXattrPrefix)] = string(value)
	}

	return metadata, nil
}

// WriteMetadata persists the given key/value pairs as xattrs under the
// MetadataXattrPrefix namespace. Existing xattrs in that namespace that are
// not present in metadata are left untouched.
//
// Writes are not atomic: a mid-loop syscall failure leaves earlier keys
// persisted on disk. Callers should ValidateMetadata first so partial
// failures only happen for genuine filesystem errors.
//
// Safe to call before or after chown — `user.*` xattrs are preserved across
// ownership changes, and envd runs as root inside the VM so the kernel
// write-permission check is satisfied regardless of file ownership.
func WriteMetadata(path string, metadata map[string]string) error {
	if err := ValidateMetadata(metadata); err != nil {
		return err
	}

	for k, v := range metadata {
		if err := unix.Setxattr(path, MetadataXattrPrefix+k, []byte(v), 0); err != nil {
			return err
		}
	}

	return nil
}

// listxattr returns the list of xattr names on the given path. The kernel's
// size-then-fetch interface races with concurrent xattr writes, so we retry
// on ERANGE.
func listxattr(path string) ([]string, error) {
	for {
		size, err := unix.Listxattr(path, nil)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return nil, nil
		}

		buf := make([]byte, size)
		n, err := unix.Listxattr(path, buf)
		if err == nil {
			return splitNullTerminated(buf[:n]), nil
		}
		if !errors.Is(err, unix.ERANGE) {
			return nil, err
		}
	}
}

// getxattr returns the value of a single xattr. Like listxattr, it retries
// on ERANGE to tolerate concurrent xattr writes growing the value between
// the size query and the fetch.
func getxattr(path, name string) ([]byte, error) {
	for {
		size, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			// Empty xattr value. Passing a zero-length buffer to Getxattr
			// would be interpreted by the kernel as another size query,
			// which could return a non-zero size if the value grew
			// concurrently and panic on buf[:n]. Return early instead.
			return []byte{}, nil
		}

		buf := make([]byte, size)
		n, err := unix.Getxattr(path, name, buf)
		if err == nil {
			return buf[:n], nil
		}
		if !errors.Is(err, unix.ERANGE) {
			return nil, err
		}
	}
}

func splitNullTerminated(buf []byte) []string {
	var out []string
	start := 0
	for i, b := range buf {
		if b == 0 {
			if i > start {
				out = append(out, string(buf[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(buf) {
		out = append(out, string(buf[start:]))
	}

	return out
}

func isXattrUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
