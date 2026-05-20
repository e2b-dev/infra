package filesystem

import (
	"errors"
	"fmt"
	"strings"
)

// MetadataXattrPrefix is the xattr namespace used to store user-defined
// metadata that envd surfaces via its file-related APIs.
const MetadataXattrPrefix = "user."

// MaxMetadataKeyLen caps the length of a metadata key. The Linux VFS limits
// the full xattr name (including the `user.` prefix) to 255 bytes.
const MaxMetadataKeyLen = 255 - len(MetadataXattrPrefix)

// MaxMetadataValueLen caps the length of a metadata value. The kernel allows
// up to 64KiB per xattr; we cap at 32KiB so the request stays well within
// per-filesystem limits (ext4/xfs/btrfs all accept this).
const MaxMetadataValueLen = 32 * 1024

// ValidateMetadata returns an error if any key/value pair would be rejected
// by WriteMetadata. Validation is filesystem-agnostic and catches obvious
// client mistakes before we issue any syscalls.
func ValidateMetadata(metadata map[string]string) error {
	for k, v := range metadata {
		if k == "" {
			return errors.New("metadata key must not be empty")
		}
		if len(k) > MaxMetadataKeyLen {
			return fmt.Errorf("metadata key %q exceeds %d bytes", k, MaxMetadataKeyLen)
		}
		if strings.ContainsRune(k, 0) {
			return fmt.Errorf("metadata key %q must not contain NUL bytes", k)
		}
		if len(v) > MaxMetadataValueLen {
			return fmt.Errorf("metadata value for key %q exceeds %d bytes", k, MaxMetadataValueLen)
		}
	}

	return nil
}
