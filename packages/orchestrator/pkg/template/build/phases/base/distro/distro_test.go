package distro

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// Golden lines lifted VERBATIM from the pre-change provision.sh so the debian
// profile reproduces them and Debian/Ubuntu behaviour is preserved.
const (
	goldenDebianPackages = "systemd systemd-sysv openssh-server sudo chrony socat curl ca-certificates fuse3 iptables git nfs-common less nftables iputils-ping jq"
	goldenDebianQuery    = `dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q "install ok installed"`
	goldenDebianInit     = "/lib/systemd/systemd"
)

func profileByKey(t *testing.T, key string) Profile {
	t.Helper()
	for _, p := range Profiles {
		if p.Key == key {
			return p
		}
	}
	t.Fatalf("no profile with key %q", key)

	return Profile{}
}

// The debian profile preserves today's Debian package set / query / init path.
func TestDebianPreserved(t *testing.T) {
	t.Parallel()
	p := profileByKey(t, "debian")
	if got := strings.Join(p.Packages, " "); got != goldenDebianPackages {
		t.Errorf("debian packages drifted:\n got: %s\nwant: %s", got, goldenDebianPackages)
	}
	if p.PkgQueryBody != goldenDebianQuery {
		t.Errorf("debian query drifted:\n got: %s\nwant: %s", p.PkgQueryBody, goldenDebianQuery)
	}
	if p.InitBinary != goldenDebianInit {
		t.Errorf("debian init drifted: got %s want %s", p.InitBinary, goldenDebianInit)
	}
	if p.TimeSyncUnit != "chrony" || p.AdminGroup != "sudo" {
		t.Errorf("debian unit/group drifted: %s / %s", p.TimeSyncUnit, p.AdminGroup)
	}
}

// The families genuinely diverge on the axes that matter.
func TestFamiliesDiffer(t *testing.T) {
	t.Parallel()
	rhel := profileByKey(t, "rhel")
	if rhel.TimeSyncUnit != "chronyd" || rhel.AdminGroup != "wheel" {
		t.Errorf("rhel unit/group wrong: %s / %s", rhel.TimeSyncUnit, rhel.AdminGroup)
	}
	// Must both regenerate the trust store AND materialize the bundle at the
	// Debian-named path envd expects — update-ca-trust alone never creates
	// ca-certificates.crt, which left envd.service unable to start on Fedora.
	if !strings.Contains(rhel.CARefresh, "update-ca-trust extract") ||
		!strings.Contains(rhel.CARefresh, `ln -sf /etc/pki/tls/certs/ca-bundle.crt "$E2B_CA_BUNDLE"`) {
		t.Errorf("rhel CA refresh wrong: %s", rhel.CARefresh)
	}
	if rhel.InitBinary != "/usr/lib/systemd/systemd" {
		t.Errorf("rhel init path wrong: %s", rhel.InitBinary)
	}
	arch := profileByKey(t, "arch")
	if !strings.Contains(arch.PkgInstall, "pacman") {
		t.Errorf("arch install should use pacman: %s", arch.PkgInstall)
	}
}

// dnf5 (Fedora 41+) only accepts --allowerasing after the subcommand and exits 2
// with "Unknown argument" when it comes first, which silently demoted every
// modern Fedora build to the flagless microdnf/yum fallback.
func TestRhelAllowErasingFollowsSubcommand(t *testing.T) {
	t.Parallel()
	rhel := profileByKey(t, "rhel")
	if !strings.Contains(rhel.PkgInstall, "install --allowerasing") {
		t.Errorf("rhel install must place --allowerasing after the subcommand: %s", rhel.PkgInstall)
	}
	if strings.Contains(rhel.PkgInstall, "--allowerasing install") {
		t.Errorf("rhel install has --allowerasing before the subcommand, which dnf5 rejects: %s", rhel.PkgInstall)
	}
}

// Alpine is supported via the OpenRC track — and it must be the OpenRC
// profile, never folded into a systemd family. (Selection-text assertions
// live in base/provision_test.go against the rendered script.)
func TestAlpineIsOpenRC(t *testing.T) {
	t.Parallel()
	alpine := profileByKey(t, "alpine")
	if alpine.Init != InitOpenRC {
		t.Errorf("alpine must be the OpenRC profile, got init %q", alpine.Init)
	}
}

// Every profile declares a known init system with a rendered setup body, and
// no body leaks another init system's tooling (systemctl in OpenRC or
// rc-update in systemd would fail at provisioning time).
func TestInitSystemsDeclaredAndCoherent(t *testing.T) {
	t.Parallel()
	for _, p := range Profiles {
		setup, ok := initSetup[p.Init]
		if !ok {
			t.Errorf("profile %q declares init %q with no setup body", p.Key, p.Init)

			continue
		}
		switch p.Init {
		case InitSystemd:
			if strings.Contains(setup, "rc-update") {
				t.Errorf("systemd init setup leaks rc-update (profile %q)", p.Key)
			}
		case InitOpenRC:
			if strings.Contains(setup, "systemctl") {
				t.Errorf("openrc init setup leaks systemctl (profile %q)", p.Key)
			}
		}
	}
}

// The per-profile fragments are spliced into single-line generated shell
// functions (`e2b_pkg_install() { … ; }`), so they stay one-liners — chain with
// `;` or `&&` instead of embedding newlines.
func TestProfileFragmentsAreSingleLine(t *testing.T) {
	t.Parallel()
	for _, p := range Profiles {
		for name, frag := range map[string]string{
			"PkgQueryBody": p.PkgQueryBody,
			"PkgInstall":   p.PkgInstall,
			"CARefresh":    p.CARefresh,
		} {
			if strings.Contains(frag, "\n") {
				t.Errorf("profile %q %s spans lines; chain with ; or && instead: %s", p.Key, name, frag)
			}
		}
	}
}

// Both init families must wire the boot-time chrony source selector in, each
// with its own mechanism: chrony.conf includes a file only that selector writes,
// so a family that skips the wiring boots with no time source at all.
func TestInitSetupWiresChronySourceSelector(t *testing.T) {
	t.Parallel()
	systemd := initSetup[InitSystemd]
	// A drop-in on the family's own unit name, not a static symlink or a unit
	// [Install] section: the name differs per family and the RHEL preset policy
	// deletes enablement symlinks on first boot.
	for _, want := range []string{
		`/etc/systemd/system/$E2B_TIMESYNC_UNIT.service.d`,
		"Requires=e2b-chrony-source.service",
		"After=e2b-chrony-source.service",
	} {
		if !strings.Contains(systemd, want) {
			t.Errorf("systemd init setup missing chrony source wiring %q", want)
		}
	}
	openrc := initSetup[InitOpenRC]
	for _, want := range []string{
		"cp /usr/local/share/e2b/chrony-source.openrc /etc/init.d/e2b-chrony-source",
		"rc-update add e2b-chrony-source boot",
	} {
		if !strings.Contains(openrc, want) {
			t.Errorf("openrc init setup missing chrony source wiring %q", want)
		}
	}
}

// Alpine's chrony build takes a SIGSYS under the seccomp filter its OpenRC init
// script hardcodes (-F 1) the moment the PHC refclock is driven. Since
// e2b-chrony-source picks the source at BOOT, provisioning cannot know whether
// the PHC branch will be taken, so the filter must come off unconditionally —
// gating it on a build-time /dev/ptp0 probe (as the original fix did) puts the
// crash back on exactly the nodes that have a PHC.
func TestOpenRCDisablesChronySeccompRegardlessOfSource(t *testing.T) {
	t.Parallel()
	openrc := initSetup[InitOpenRC]
	if !strings.Contains(openrc, `echo 'command_args="-F 0"' >>"/etc/conf.d/$E2B_TIMESYNC_UNIT"`) {
		t.Error("openrc init setup must disable the chronyd seccomp filter via conf.d command_args")
	}
	// The whole block runs on every Alpine build, so no COMMAND in it may branch
	// on the device or on a provisioning-time PHC verdict. Comments are stripped
	// first — they are where the reasoning lives and legitimately say "refclock".
	var code []string
	for l := range strings.SplitSeq(openrc, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			code = append(code, l)
		}
	}
	openrcCode := strings.Join(code, "\n")
	for _, bad := range []string{"/dev/ptp0", "E2B_CHRONY_PHC", "refclock"} {
		if strings.Contains(openrcCode, bad) {
			t.Errorf("openrc init setup branches on %q; the time source is chosen at boot, not while provisioning", bad)
		}
	}
}

// The cache fingerprint must cover the registry's whole contribution to
// provisioning: stable across calls, and carrying both the view data and the
// explicit Version (a profile or init-file change must rotate the base-layer
// cache key; the script structure is hashed separately in base).
func TestFingerprintStableAndVersioned(t *testing.T) {
	t.Parallel()
	a, b := Fingerprint(), Fingerprint()
	if a != b || len(a) != 64 {
		t.Errorf("fingerprint must be a stable sha256 hex: %q vs %q", a, b)
	}
	want := sha256.Sum256([]byte(Version + "\x00" + fmt.Sprintf("%#v", NewTemplateData())))
	if a != hex.EncodeToString(want[:]) {
		t.Error("fingerprint must hash Version + template view data")
	}
}

// Sanity: the RHEL-family rebuilds (centos/rocky/alma) all resolve to one arm.
func TestRHELFamilyAliases(t *testing.T) {
	t.Parallel()
	rhel := profileByKey(t, "rhel")
	for _, want := range []string{"fedora", "centos", "rocky", "almalinux"} {
		found := false
		for _, id := range rhel.IDs {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Errorf("rhel family missing alias %q", want)
		}
	}
}

// RHEL, Oracle Linux and Amazon Linux are deliberately NOT accepted: sandboxes
// boot E2B's kernel, so the kernel-level fidelity those images are chosen for
// isn't there. Rejecting them up front beats building something subtly wrong.
func TestKernelDependentIDsAreRejected(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"rhel", "ol", "amzn"} {
		for _, got := range SupportedIDs() {
			if got == id {
				t.Errorf("id %q must not be accepted: E2B supplies the guest kernel", id)
			}
		}
	}
}

// Quoted view fields are spliced into sh double quotes via Go %q, which only
// matches sh semantics for values free of `"`, `\`, `$`, backticks and control
// characters (%q passes $ and backtick through unescaped — sh would expand
// them — and escapes control chars into sequences sh reads literally).
func TestQuotedFieldsAreShellSafe(t *testing.T) {
	t.Parallel()
	for _, p := range Profiles {
		fields := map[string]string{
			"Packages":     strings.Join(p.Packages, " "),
			"InitBinary":   p.InitBinary,
			"TimeSyncUnit": p.TimeSyncUnit,
			"SSHUnit":      p.SSHUnit,
			"AdminGroup":   p.AdminGroup,
			"CABundle":     p.CABundle,
			"InitSystem":   string(p.Init),
		}
		for name, v := range fields {
			if fmt.Sprintf("%q", v) != `"`+v+`"` {
				t.Errorf("profile %q field %s needs %%q escaping — not plain-sh-quotable: %q", p.Key, name, v)
			}
			if strings.ContainsAny(v, "$`") {
				t.Errorf("profile %q field %s contains sh-expandable characters: %q", p.Key, name, v)
			}
		}
	}
}

// The view hands provision.sh ready-made patterns and lists.
func TestTemplateDataJoins(t *testing.T) {
	t.Parallel()
	data := NewTemplateData()
	if len(data.Profiles) != len(Profiles) {
		t.Fatalf("view has %d profiles, registry %d", len(data.Profiles), len(Profiles))
	}
	for i, p := range Profiles {
		if data.Profiles[i].CasePattern != strings.Join(p.IDs, "|") {
			t.Errorf("profile %q CasePattern mismatch: %q", p.Key, data.Profiles[i].CasePattern)
		}
	}
	if data.RejectedIDsPattern != strings.Join(RejectedIDs, "|") {
		t.Errorf("RejectedIDsPattern mismatch: %q", data.RejectedIDsPattern)
	}
	if data.SupportedIDs != strings.Join(SupportedIDs(), ", ") {
		t.Errorf("SupportedIDs mismatch: %q", data.SupportedIDs)
	}
}
