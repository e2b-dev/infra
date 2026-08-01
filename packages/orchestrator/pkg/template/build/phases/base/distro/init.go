// Init-system axis of the distro profiles: one provisioning-time shell block
// per init system (init-*.sh), rendered into the selector as e2b_init_setup()
// so provision.sh stays init-agnostic.
package distro

import (
	_ "embed"
	"strings"
)

// InitSystem is the guest init family a profile boots with.
type InitSystem string

const (
	// InitSystemd — Debian/Ubuntu, RHEL/Fedora, Arch (systemd via envd.service).
	InitSystemd InitSystem = "systemd"
	// InitOpenRC — Alpine (busybox init → OpenRC via the baked /etc/init.d/envd).
	InitOpenRC InitSystem = "openrc"
	// InitNixOS — premade NixOS (declarative; provisioning masks nothing).
	InitNixOS InitSystem = "nixos"
)

//go:embed init-systemd.sh
var initSystemdSh string

//go:embed init-openrc.sh
var initOpenRCSh string

//go:embed init-nixos.sh
var initNixOSSh string

// initSetup is the provisioning-time shell block per init system. Bodies may
// reference the selector's profile variables (e.g. $E2B_TIMESYNC_UNIT), defined
// in the same case arm before the function is called. The trailing newline is
// trimmed so bodies splice like the former in-Go literals.
var initSetup = map[InitSystem]string{
	InitSystemd: strings.TrimRight(initSystemdSh, "\n"),
	InitOpenRC:  strings.TrimRight(initOpenRCSh, "\n"),
	InitNixOS:   strings.TrimRight(initNixOSSh, "\n"),
}

// indentBlock indents every non-empty line of a shell block for embedding
// inside the generated selector function bodies.
func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}

	return strings.Join(lines, "\n")
}
