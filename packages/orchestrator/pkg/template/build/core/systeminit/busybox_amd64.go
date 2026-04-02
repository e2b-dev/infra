//go:build amd64

// Busybox v1.36.1 static binary for amd64 (musl, minimal ~16 applets).
// Custom build added in #1002 — origin unknown, no distro tag in binary.

package systeminit

import _ "embed"

//go:embed busybox_1.36.1-2_amd64
var BusyboxBinary []byte
