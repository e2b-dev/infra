//go:build amd64

// Busybox static binary for amd64.
// Downloaded from https://github.com/e2b-dev/fc-busybox/releases at build time.

package systeminit

import _ "embed"

//go:embed busybox
var BusyboxBinary []byte
