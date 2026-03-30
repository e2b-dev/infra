//go:build arm64

// Busybox v1.36.1 static binary for arm64 (glibc, full 271 applets).
// Source: Debian busybox-static 1:1.36.1-9 (https://packages.debian.org/busybox-static)
// TODO: rebuild both binaries from the same minimal config for consistency.

package systeminit

import _ "embed"

//go:embed busybox_1.36.1-2_arm64
var BusyboxBinary []byte
