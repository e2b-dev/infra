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
// /etc/os-release values that map to the family; PkgQueryBody, PkgInstall,
// CARefresh and Bootstrap are shell fragments spliced into the generated
// selector (Bootstrap, if set, runs first — for premade images with no FHS
// userland yet).
type Profile struct {
	Key          string
	Init         InitSystem
	IDs          []string
	Packages     []string
	PkgQueryBody string
	PkgInstall   string
	InitBinary   string
	TimeSyncUnit string
	SSHUnit      string
	AdminGroup   string
	CABundle     string
	CARefresh    string
	Bootstrap    string
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
		PkgInstall:   `apt-get -q update && DEBIAN_FRONTEND=noninteractive DEBCONF_NOWARNINGS=yes apt-get -qq -o=Dpkg::Use-Pty=0 install -y --no-install-recommends "$@"`,
		InitBinary:   "/lib/systemd/systemd",
		TimeSyncUnit: "chrony",
		SSHUnit:      "ssh",
		AdminGroup:   "sudo",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:    "update-ca-certificates",
	},
	{
		Key:  "rhel", // Fedora, CentOS Stream, Rocky, Alma
		Init: InitSystemd,
		// Deliberately not rhel/ol/amzn. Sandboxes always boot E2B's kernel, so
		// /lib/modules is empty and SELinux is off — which is most of what RHEL
		// and Oracle Linux are chosen for (kABI, signed kmods, UEK), and
		// kernel-devel would resolve against a kernel that isn't running. RHEL's
		// UBI repos also carry neither chrony nor nfs-utils. Accepting those IDs
		// promised a fidelity this can't deliver; they now fail fast instead.
		IDs: []string{"fedora", "centos", "rocky", "almalinux"},
		// "iptables" (not iptables-nft) and "tar" cover yum-era CentOS 7, which
		// lacks the nft package and doesn't ship tar.
		Packages: []string{
			"systemd", "shadow-utils", "passwd", "openssh-server", "sudo", "chrony",
			"socat", "curl", "ca-certificates", "fuse3", "iptables", "git",
			"nfs-utils", "less", "nftables", "iputils", "jq", "bash", "tar",
		},
		PkgQueryBody: `rpm -q "$1" >/dev/null 2>&1`,
		// dnf → microdnf → yum spans the family (yum for CentOS 7); errors reach
		// the build log. --allowerasing goes AFTER the subcommand: dnf5 (Fedora
		// 41+, where /usr/bin/dnf IS dnf5) rejects it before one with "Unknown
		// argument" and exits 2, which put two errors in every modern Fedora
		// build log and quietly demoted the install to the flagless
		// microdnf/yum fallback, so the flag never applied. dnf4 accepts either
		// position.
		PkgInstall:   `dnf -y install --allowerasing "$@" || microdnf -y install "$@" || yum -y install "$@"`,
		InitBinary:   "/usr/lib/systemd/systemd",
		TimeSyncUnit: "chronyd",
		SSHUnit:      "sshd",
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
		SSHUnit:      "sshd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		// Arch ships /etc/ssl/certs/ca-certificates.crt as a symlink to the p11-kit
		// bundle update-ca-trust extract regenerates — no manual link, unlike RHEL.
		CARefresh: "update-ca-trust extract",
	},
	{
		Key:  "alpine",
		Init: InitOpenRC,
		IDs:  []string{"alpine"},
		// shadow provides useradd/usermod (busybox's adduser takes different flags).
		// util-linux-misc provides ionice, which busybox does not ship: envd runs at
		// realtime IO (supervise-daemon --ionice 1:4) and resets each user process to
		// best-effort with it, so without the binary they'd inherit realtime IO.
		Packages: []string{
			"openrc", "shadow", "openssh", "sudo", "chrony", "socat", "curl",
			"ca-certificates", "fuse3", "iptables", "git", "nfs-utils", "less",
			"nftables", "iputils", "jq", "bash", "util-linux-misc",
		},
		PkgQueryBody: `apk info -e "$1" >/dev/null 2>&1`,
		PkgInstall:   `apk add --no-cache "$@"`,
		InitBinary:   "/bin/busybox",
		TimeSyncUnit: "chronyd",
		SSHUnit:      "sshd",
		AdminGroup:   "wheel",
		CABundle:     "/etc/ssl/certs/ca-certificates.crt",
		CARefresh:    "update-ca-certificates",
	},
	{
		Key:  "nixos",
		Init: InitNixOS,
		IDs:  []string{"nixos"},
		// Premade: packages and services are declared in the image's own NixOS
		// configuration, so there is no package manager to drive at build time.
		Packages:     nil,
		PkgQueryBody: "true",
		PkgInstall:   `echo "[provision] ERROR: NixOS images are premade — packages must be declared in the image's NixOS configuration" >&2; exit 1`,
		InitBinary:   "/nix/var/nix/profiles/system/init",
		TimeSyncUnit: "chronyd",
		// Left empty on purpose: services.openssh is declared in the image's
		// configuration, and the NixOS init setup never enables units.
		SSHUnit:    "",
		AdminGroup: "wheel",
		CABundle:   "/etc/ssl/certs/ca-certificates.crt",
		// The bundle appears at first activation; nothing to refresh pre-boot.
		CARefresh: `echo "NixOS: the CA bundle is provided by the image configuration at first activation; nothing to refresh at provision time"`,
		// No FHS userland pre-activation — put the baked busybox on PATH first.
		Bootstrap: `E2B_BB_DIR=/run/e2b-tools
    /usr/bin/busybox mkdir -p "$E2B_BB_DIR"
    /usr/bin/busybox --install -s "$E2B_BB_DIR"
    export PATH="$E2B_BB_DIR:$PATH"`,
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
		if p.Bootstrap != "" {
			fmt.Fprintf(&b, "    %s\n", p.Bootstrap)
		}
		fmt.Fprintf(&b, "    E2B_PACKAGES=%q\n", strings.Join(p.Packages, " "))
		fmt.Fprintf(&b, "    e2b_pkg_query() { %s; }\n", p.PkgQueryBody)
		fmt.Fprintf(&b, "    e2b_pkg_install() { %s; }\n", p.PkgInstall)
		fmt.Fprintf(&b, "    E2B_INIT_BIN=%q\n", p.InitBinary)
		fmt.Fprintf(&b, "    E2B_TIMESYNC_UNIT=%q\n", p.TimeSyncUnit)
		fmt.Fprintf(&b, "    E2B_SSH_UNIT=%q\n", p.SSHUnit)
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
