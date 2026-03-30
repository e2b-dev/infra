//go:build arm64

package systeminit

import _ "embed"

//go:embed busybox_arm64
var BusyboxBinary []byte
