package filesystem

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"github.com/pkg/xattr"
)

// MetadataXattrPrefix is the xattr namespace used to store user-defined
// metadata that envd surfaces via its file-related APIs. The `user.` prefix
// is required by the Linux VFS for unprivileged xattrs; `e2b.` namespaces
// our keys so they cannot collide with other tooling that writes to
// `user.*` on the same files.
const MetadataXattrPrefix = "user.e2b."

// MaxMetadataKeyLen caps the length of a metadata key. The Linux VFS limits
// the full xattr name (including the `user.e2b.` prefix) to 255 bytes.
const MaxMetadataKeyLen = 255 - len(MetadataXattrPrefix)

// MaxMetadataValueLen caps the length of a metadata value. ext4 stores each
// xattr value inline in a single filesystem block (4 KiB on x86_64), shared
// with the inode header and any other xattrs on the file, so the practical
// per-value ceiling is well below 4 KiB. We cap at 1 KiB to leave room for
// multiple keys per file and to stay comfortably within other filesystems'
// limits. Values larger than this are rejected with HTTP 400.
const MaxMetadataValueLen = 1024

// ValidateMetadata returns an error if any key/value pair would be rejected
// by WriteMetadata. Validation is filesystem-agnostic and catches obvious
// client mistakes before we issue any syscalls.
//
// Both keys and values are required to be printable US-ASCII (0x20-0x7E),
// matching the constraint documented for `X-Metadata-*` request headers.
func ValidateMetadata(metadata map[string]string) error {
	for k, v := range metadata {
		if k == "" {
			return errors.New("metadata key must not be empty")
		}
		if len(k) > MaxMetadataKeyLen {
			return fmt.Errorf("metadata key %q exceeds %d bytes", k, MaxMetadataKeyLen)
		}
		if err := validatePrintableASCII("key", k); err != nil {
			return err
		}
		if len(v) > MaxMetadataValueLen {
			return fmt.Errorf("metadata value for key %q exceeds %d bytes", k, MaxMetadataValueLen)
		}
		if err := validatePrintableASCII(fmt.Sprintf("value for key %q", k), v); err != nil {
			return err
		}
	}

	return nil
}

func validatePrintableASCII(label, s string) error {
	for i := range len(s) {
		c := s[i]
		if c < 0x20 || c > 0x7E {
			return fmt.Errorf("metadata %s contains non-printable-ASCII byte 0x%02x at offset %d", label, c, i)
		}
	}

	return nil
}

// ReadMetadata returns user-defined metadata stored in xattrs under the
// MetadataXattrPrefix namespace. Returns nil (not an error) when the
// filesystem does not support xattrs or the file has no metadata set.
func ReadMetadata(path string) (map[string]string, error) {
	names, err := xattr.List(path)
	if err != nil {
		if IsXattrUnsupported(err) {
			return nil, nil
		}

		return nil, err
	}

	var metadata map[string]string
	for _, name := range names {
		if !strings.HasPrefix(name, MetadataXattrPrefix) {
			continue
		}

		value, err := xattr.Get(path, name)
		if err != nil {
			if errors.Is(err, xattr.ENOATTR) {
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

// WriteMetadata replaces the file's user-defined metadata with the given
// key/value pairs. Any existing xattrs under MetadataXattrPrefix that are
// absent from metadata are removed, and the supplied keys are written via
// xattr.Set. Passing a nil/empty map therefore clears all metadata, so that
// overwriting a file (which preserves xattrs across O_TRUNC) does not leak
// stale metadata from a previous upload.
//
// Safe to call before or after chown — `user.*` xattrs are preserved across
// ownership changes, and envd runs as root inside the VM so the kernel
// write-permission check is satisfied regardless of file ownership.
func WriteMetadata(path string, metadata map[string]string) error {
	if err := ValidateMetadata(metadata); err != nil {
		return err
	}

	existing, err := xattr.List(path)
	if err != nil {
		return err
	}

	for _, name := range existing {
		if !strings.HasPrefix(name, MetadataXattrPrefix) {
			continue
		}
		key := strings.TrimPrefix(name, MetadataXattrPrefix)
		if _, keep := metadata[key]; keep {
			continue
		}
		if err := xattr.Remove(path, name); err != nil && !errors.Is(err, xattr.ENOATTR) {
			return err
		}
	}

	for k, v := range metadata {
		if err := xattr.Set(path, MetadataXattrPrefix+k, []byte(v)); err != nil {
			return err
		}
	}

	return nil
}

// IsXattrUnsupported reports whether err indicates the filesystem does not
// support extended attributes (e.g. virtual filesystems such as /proc and
// /sys). Callers that persist metadata best-effort can use this to log and
// continue instead of failing the request.
func IsXattrUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}
