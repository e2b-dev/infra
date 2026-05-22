//go:build linux

package filesystem

import (
	"errors"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// xattrNameSeparator separates the null-terminated names returned by
// listxattr(2).
const xattrNameSeparator = "\x00"

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

func listxattr(path string) ([]string, error) {
	size, err := unix.Listxattr(path, nil)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}

	buf := make([]byte, size)
	n, err := unix.Listxattr(path, buf)
	if err != nil {
		return nil, err
	}

	// listxattr(2) returns names as `name1\0name2\0...\0`;
	// drop the trailing terminator before splitting
	s := strings.TrimRight(string(buf[:n]), xattrNameSeparator)
	if s == "" {
		return nil, nil
	}

	return strings.Split(s, xattrNameSeparator), nil
}

func getxattr(path, name string) ([]byte, error) {
	size, err := unix.Getxattr(path, name, nil)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, size)
	n, err := unix.Getxattr(path, name, buf)
	if err != nil {
		return nil, err
	}

	return buf[:n], nil
}

func isXattrUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
