// Package distro makes template-build provisioning distro-aware (ADR-010 /
// FEAT-145 / IMPL-145 W1). It keys on the base image's DECLARED identity — its
// /etc/os-release ID — rather than probing which package-manager binary happens
// to exist. Each supported distribution is a declared Profile; the base-phase
// provisioning script selects the right profile by os-release ID in-guest, so
// the whole divergence between distros lives in one data table here instead of
// as scattered runtime detection.
//
// Scope: the systemd family (Debian/Ubuntu, Fedora/RHEL/CentOS/Rocky/Alma,
// Arch) plus Alpine on the OpenRC track (IMPL-145 W5). Distros with no
// os-release identity or no declared profile are rejected with a clear error.
package distro

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Version is the explicit provisioning-contract version, folded into
// Fingerprint. Bump it to force a base-layer rebuild for changes the
// generated selector text cannot capture.
const Version = "1"

// Fingerprint is a stable hash of the whole generated provisioning contract
// (profiles, init-system blocks, selector, Version). It feeds the base-layer
// cache key: any profile or init-setup change MUST rotate the key, or already
// provisioned bases built from the old contract get silently reused
// (IMPL-145 qa.md QA11 — this exact staleness poisoned dev twice).
func Fingerprint() string {
	sum := sha256.Sum256([]byte(Version + "\x00" + ShellSelector()))

	return hex.EncodeToString(sum[:])
}

// Profile is the declared, per-family provisioning contract. Everything that
// differs across distributions is data here — never discovered at runtime.
type Profile struct {
	// Key is the canonical family key.
	Key string
	// Init is the init family the guest boots with; it selects the
	// e2b_init_setup() body rendered into the selector (see init.go).
	Init InitSystem
	// IDs are the /etc/os-release ID values that map to this family.
	IDs []string
	// Packages is the required package set, in this family's package names.
	Packages []string
	// PkgQueryBody is the body of a shell function testing whether "$1" is installed.
	PkgQueryBody string
	// PkgInstall installs the packages passed as "$@".
	PkgInstall string
	// InitBinary is symlinked to /usr/sbin/init.
	InitBinary string
	// TimeSyncUnit is the chrony systemd unit name (differs: chrony vs chronyd).
	TimeSyncUnit string
	// AdminGroup is the passwordless-sudo group (sudo on Debian, wheel elsewhere).
	AdminGroup string
	// CABundle is the trust-store path envd expects.
	CABundle string
	// CARefresh regenerates the trust store (differs: update-ca-certificates vs update-ca-trust).
	CARefresh string
}

// Profiles is the declared registry (systemd family, v1). The package names,
// unit names and CA paths reuse the mapping validated in infra #2941; the
// *structure* (declared profile keyed on distro, not detected package manager)
// is deliberately different — see ADR-010.
var Profiles = []Profile{
	{
		Key:  "debian",
		Init: InitSystemd,
		IDs:  []string{"debian", "ubuntu"},
		Packages: []string{
			"systemd", "systemd-sysv", "openssh-server", "sudo", "chrony", "socat",
			"curl", "ca-certificates", "fuse3", "iptables", "git", "nfs-common",
			"less", "nftables", "iputils-ping", "jq",
		},
		PkgQueryBody: `dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"`,
		PkgInstall:   "apt-get -q update\n    DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends \"$@\"",
		InitBinary:   "/lib/systemd/systemd",
		TimeSyncUnit: "chrony",
		AdminGroup:   "sudo",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:    "update-ca-certificates",
	},
	{
		Key:  "rhel",
		Init: InitSystemd,
		// Fedora, RHEL, CentOS Stream, Rocky, Alma, Oracle Linux, Amazon Linux.
		IDs: []string{"fedora", "rhel", "centos", "rocky", "almalinux", "ol", "amzn"},
		Packages: []string{
			"systemd", "shadow-utils", "passwd", "openssh-server", "sudo", "chrony",
			"socat", "curl", "ca-certificates", "fuse3", "iptables-nft", "git",
			"nfs-utils", "less", "nftables", "iputils", "jq", "bash",
		},
		PkgQueryBody: `rpm -q "$1" >/dev/null 2>&1`,
		PkgInstall:   `dnf -y --allowerasing install "$@" 2>/dev/null || microdnf -y install "$@"`,
		InitBinary:   "/usr/lib/systemd/systemd",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		// update-ca-trust regenerates /etc/pki/ca-trust/extracted/* but never
		// creates a file named ca-certificates.crt (that name is Debian's), so
		// the bundle envd expects must be linked to the extracted PEM bundle
		// explicitly — otherwise envd.service's ExecStartPre finds no bundle
		// and its update-ca-certificates fallback doesn't exist on this family.
		CARefresh: `update-ca-trust extract && ln -sf /etc/pki/tls/certs/ca-bundle.crt "$E2B_CA_BUNDLE"`,
	},
	{
		Key:  "arch",
		Init: InitSystemd,
		IDs:  []string{"arch", "archarm"},
		Packages: []string{
			"systemd", "shadow", "openssh", "sudo", "chrony", "socat", "curl",
			"ca-certificates", "fuse3", "iptables", "git", "nfs-utils", "less",
			"nftables", "iputils", "jq", "bash",
		},
		PkgQueryBody: `pacman -Q "$1" >/dev/null 2>&1`,
		PkgInstall:   "pacman -Sy --noconfirm\n    pacman -S --noconfirm --needed \"$@\"",
		InitBinary:   "/usr/lib/systemd/systemd",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		// Arch ships p11-kit's update-ca-trust (no update-ca-certificates);
		// its extract step does emit /etc/ssl/certs/ca-certificates.crt.
		CARefresh: "update-ca-trust extract",
	},
	{
		Key:  "alpine",
		Init: InitOpenRC,
		IDs:  []string{"alpine"},
		// No systemd packages: Alpine boots busybox init → OpenRC. shadow
		// provides the useradd/usermod the build steps use (busybox's
		// adduser takes different flags).
		Packages: []string{
			"openrc", "shadow", "openssh", "sudo", "chrony", "socat", "curl",
			"ca-certificates", "fuse3", "iptables", "git", "nfs-utils", "less",
			"nftables", "iputils", "jq", "bash",
		},
		PkgQueryBody: `apk info -e "$1" >/dev/null 2>&1`,
		PkgInstall:   `apk add --no-cache "$@"`,
		// busybox init is Alpine's standard PID1; it hands off to OpenRC via
		// the inittab installed by e2b_init_setup (init.go).
		InitBinary:   "/bin/busybox",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		// Alpine's ca-certificates ships a Debian-compatible
		// update-ca-certificates that writes the bundle at CABundle.
		CARefresh: "update-ca-certificates",
	},
}

// SupportedIDs returns every os-release ID the v1 selector accepts.
func SupportedIDs() []string {
	var ids []string
	for _, p := range Profiles {
		ids = append(ids, p.IDs...)
	}
	return ids
}

// ShellSelector generates the POSIX-sh block that provision.sh sources: it
// selects the profile by the guest's own $E2B_DISTRO_ID (set from /etc/os-release)
// and defines the profile's packages, package functions, init path, time-sync
// unit, admin group and CA handling. An unrecognized distro exits 1 with a clear,
// customer-visible error (FEAT-145 AC4) — never a silent best-effort.
//
// Selection is by DECLARED distro identity, not by `command -v <pkgmgr>` — that
// is the whole point of ADR-010 and the reason this is not infra #2941.
func ShellSelector() string {
	var b strings.Builder
	b.WriteString(`case "$E2B_DISTRO_ID" in` + "\n")
	for _, p := range Profiles {
		fmt.Fprintf(&b, "  %s)\n", strings.Join(p.IDs, "|"))
		fmt.Fprintf(&b, "    E2B_PACKAGES=%q\n", strings.Join(p.Packages, " "))
		fmt.Fprintf(&b, "    e2b_pkg_query() { %s; }\n", p.PkgQueryBody)
		fmt.Fprintf(&b, "    e2b_pkg_install() { %s; }\n", p.PkgInstall)
		fmt.Fprintf(&b, "    E2B_INIT_BIN=%q\n", p.InitBinary)
		fmt.Fprintf(&b, "    E2B_TIMESYNC_UNIT=%q\n", p.TimeSyncUnit)
		fmt.Fprintf(&b, "    E2B_ADMIN_GROUP=%q\n", p.AdminGroup)
		fmt.Fprintf(&b, "    E2B_CA_BUNDLE=%q\n", p.CABundle)
		fmt.Fprintf(&b, "    e2b_ca_refresh() { %s; }\n", p.CARefresh)
		fmt.Fprintf(&b, "    E2B_INIT_SYSTEM=%q\n", p.Init)
		fmt.Fprintf(&b, "    e2b_init_setup() {\n%s\n    }\n", indentBlock(initSetup[p.Init], "        "))
		fmt.Fprintf(&b, "    ;;\n")
	}
	fmt.Fprintf(&b, "  *)\n")
	fmt.Fprintf(&b, "    echo \"[provision] ERROR: unsupported base image distribution: ID='${E2B_DISTRO_ID:-unknown}'.\" >&2\n")
	fmt.Fprintf(&b, "    echo \"[provision] E2B template builds support: %s.\" >&2\n", strings.Join(SupportedIDs(), ", "))
	fmt.Fprintf(&b, "    exit 1\n")
	fmt.Fprintf(&b, "    ;;\n")
	b.WriteString("esac\n")
	return b.String()
}
