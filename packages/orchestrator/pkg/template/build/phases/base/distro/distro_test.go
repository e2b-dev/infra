package distro

import (
	"strings"
	"testing"
)

// Golden lines lifted VERBATIM from the pre-change provision.sh
// (packages/orchestrator/pkg/template/build/phases/base/provision.sh @ infra main).
// The debian profile must reproduce these so Debian/Ubuntu behaviour is preserved
// (FEAT-145 AC2).
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

// AC2: the debian profile preserves today's Debian package set / query / init path.
func TestDebianPreserved(t *testing.T) {
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
	rhel := profileByKey(t, "rhel")
	if rhel.TimeSyncUnit != "chronyd" || rhel.AdminGroup != "wheel" {
		t.Errorf("rhel unit/group wrong: %s / %s", rhel.TimeSyncUnit, rhel.AdminGroup)
	}
	if rhel.CARefresh != "update-ca-trust extract" {
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

// The generated selector keys on the DECLARED distro id, never on which
// package-manager binary exists (the anti-#2941 invariant, TT-2).
func TestSelectorNoPackageManagerProbing(t *testing.T) {
	sel := ShellSelector()
	for _, bad := range []string{
		"command -v apt-get", "command -v dnf", "command -v yum",
		"command -v microdnf", "command -v pacman", "PKG_FAMILY",
	} {
		if strings.Contains(sel, bad) {
			t.Errorf("selector leaked package-manager probing: %q", bad)
		}
	}
	if !strings.Contains(sel, `case "$E2B_DISTRO_ID" in`) {
		t.Error("selector must switch on $E2B_DISTRO_ID (declared distro identity)")
	}
}

// Every supported id gets a case arm; an unknown id hits the failing default (AC4).
func TestSelectorCoversIDsAndRejects(t *testing.T) {
	sel := ShellSelector()
	for _, id := range SupportedIDs() {
		if !strings.Contains(sel, id) {
			t.Errorf("selector missing arm for supported id %q", id)
		}
	}
	for _, want := range []string{"*)", "unsupported base image", "exit 1"} {
		if !strings.Contains(sel, want) {
			t.Errorf("selector missing fast-reject piece %q", want)
		}
	}
	// Alpine must NOT be accepted at v1 (it belongs to the OpenRC track, W5).
	for _, p := range Profiles {
		for _, id := range p.IDs {
			if id == "alpine" {
				t.Error("alpine must not be a v1 systemd-family id")
			}
		}
	}
}

// Sanity: RHEL-family aliases (rocky/alma/oracle/amazon) all resolve to one arm.
func TestRHELFamilyAliases(t *testing.T) {
	rhel := profileByKey(t, "rhel")
	for _, want := range []string{"fedora", "rhel", "centos", "rocky", "almalinux", "ol", "amzn"} {
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
