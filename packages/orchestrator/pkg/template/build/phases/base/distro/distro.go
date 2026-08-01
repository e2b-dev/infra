// Package distro makes template-build provisioning distro-aware: it selects a
// declared per-family Profile by the base image's /etc/os-release ID rather than
// probing for a package manager. Supported: the systemd family (Debian/Ubuntu,
// Fedora/CentOS/Rocky/Alma, Arch) and Alpine on OpenRC. Derivatives are
// provisioned via ID_LIKE as a best-effort guess with a warning; kernel-dependent
// ids (RejectedIDs) and anything unmatched are rejected with a clear error.
package distro

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Version forces a base-layer rebuild for provisioning changes the view data
// can't otherwise capture (e.g. how provision.sh consumes a field); bump it
// when the contract changes. "2": selection moved into provision.sh's template.
const Version = "2"

// Fingerprint hashes the distro registry's entire contribution to the
// provisioning script — the view data, which embeds the init-setup files, the
// quoted profile scalars and the id lists. The script's structure is the raw
// provision.sh template, hashed separately by base's Hash(). %#v prints field
// names, so a field added to ProfileView is fingerprinted automatically.
func Fingerprint() string {
	sum := sha256.Sum256([]byte(Version + "\x00" + fmt.Sprintf("%#v", NewTemplateData())))

	return hex.EncodeToString(sum[:])
}

// Profile is the declared, per-family provisioning contract. IDs are the
// /etc/os-release values that map to the family; PkgQueryBody, PkgInstall,
// CARefresh and Bootstrap are shell fragments spliced into provision.sh's
// selection template (Bootstrap, if set, runs first — for premade images with
// no FHS userland yet).
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
		// Not $toplevel/init: from NixOS 25.05 that IS the systemd binary, and
		// activation moved into the systemd stage-1 initrd. We boot the rootfs
		// directly with no initrd, so PID 1 would start against an unpopulated
		// /etc and freeze on "Unit default.target not found". The image ships
		// this shim, which activates and then execs systemd — what the pre-25.05
		// stage-2 init did (nixos-base-image/build.sh).
		InitBinary:   "/sbin/e2b-nixos-init",
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

// SupportedIDs returns every os-release ID the selection accepts.
func SupportedIDs() []string {
	var ids []string
	for _, p := range Profiles {
		ids = append(ids, p.IDs...)
	}

	return ids
}

// RejectedIDs are distro ids we refuse even though their ID_LIKE names a
// family we do support. Oracle Linux and Amazon Linux both declare
// ID_LIKE=fedora, so without this provision.sh's ID_LIKE fallback would quietly
// re-admit exactly the images the rhel profile documents as out of scope.
var RejectedIDs = []string{"rhel", "ol", "amzn"}
