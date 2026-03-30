//go:build amd64

package systeminit

import _ "embed"

//go:embed busybox_amd64
var BusyboxBinary []byte
