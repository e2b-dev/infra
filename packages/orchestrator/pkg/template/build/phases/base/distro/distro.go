// Package distro makes template-build provisioning distro-aware: it selects a
// declared per-family Profile by the base image's /etc/os-release ID rather than
// probing for a package manager. Supported: the systemd family (Debian/Ubuntu,
// Fedora/RHEL/CentOS/Rocky/Alma, Arch) and Alpine on OpenRC; anything else is
// rejected with a clear error.
package distro

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Version forces a base-layer rebuild for provisioning changes the generated
// selector text can't otherwise capture; bump it when the contract changes.
const Version = "1"

// Fingerprint hashes the whole generated provisioning contract into the
// base-layer cache key, so any profile or init-setup change rebuilds the base.
func Fingerprint() string {
	sum := sha256.Sum256([]byte(Version + "\x00" + ShellSelector()))

	return hex.EncodeToString(sum[:])
}

// Profile is the declared, per-family provisioning contract. IDs are the
// /etc/os-release values that map to the family; PkgQueryBody, PkgInstall and
// CARefresh are shell fragments spliced into the generated selector.
type Profile struct {
	Key          string
	Init         InitSystem
	IDs          []string
	Packages     []string
	PkgQueryBody string
	PkgInstall   string
	InitBinary   string
	TimeSyncUnit string
	AdminGroup   string
	CABundle     string
	CARefresh    string
}

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
		Key:  "rhel", // Fedora, RHEL, CentOS Stream, Rocky, Alma, Oracle Linux, Amazon Linux
		Init: InitSystemd,
		IDs:  []string{"fedora", "rhel", "centos", "rocky", "almalinux", "ol", "amzn"},
		// "iptables" (not iptables-nft) and "tar" cover the yum-era members
		// (CentOS 7, Amazon Linux 2) that lack the nft package or don't ship tar.
		Packages: []string{
			"systemd", "shadow-utils", "passwd", "openssh-server", "sudo", "chrony",
			"socat", "curl", "ca-certificates", "fuse3", "iptables", "git",
			"nfs-utils", "less", "nftables", "iputils", "jq", "bash", "tar",
		},
		PkgQueryBody: `rpm -q "$1" >/dev/null 2>&1`,
		// dnf → microdnf → yum spans the whole family; errors reach the build log.
		PkgInstall:   `dnf -y --allowerasing install "$@" || microdnf -y install "$@" || yum -y install "$@"`,
		InitBinary:   "/usr/lib/systemd/systemd",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		// update-ca-trust never writes ca-certificates.crt (a Debian name), so
		// link the bundle envd's ExecStartPre expects to the extracted PEM.
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
		// -Syu (not -Sy then -S): Arch documents partial upgrades as unsupported.
		PkgInstall:   `pacman -Syu --noconfirm --needed "$@"`,
		InitBinary:   "/usr/lib/systemd/systemd",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:    "update-ca-trust extract",
	},
	{
		Key:  "alpine",
		Init: InitOpenRC,
		IDs:  []string{"alpine"},
		// shadow provides useradd/usermod (busybox's adduser takes different flags).
		Packages: []string{
			"openrc", "shadow", "openssh", "sudo", "chrony", "socat", "curl",
			"ca-certificates", "fuse3", "iptables", "git", "nfs-utils", "less",
			"nftables", "iputils", "jq", "bash",
		},
		PkgQueryBody: `apk info -e "$1" >/dev/null 2>&1`,
		PkgInstall:   `apk add --no-cache "$@"`,
		InitBinary:   "/bin/busybox",
		TimeSyncUnit: "chronyd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:    "update-ca-certificates",
	},
}

// SupportedIDs returns every os-release ID the selector accepts.
func SupportedIDs() []string {
	var ids []string
	for _, p := range Profiles {
		ids = append(ids, p.IDs...)
	}

	return ids
}

// ShellSelector generates the POSIX-sh block provision.sh sources: it switches
// on the guest's $E2B_DISTRO_ID and defines the profile's packages, shell
// functions, init path, time-sync unit, admin group and CA handling. An
// unrecognized id exits 1 with a customer-visible error.
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
