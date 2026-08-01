// No build tag on purpose — these tests must run on darwin too.
package base

import (
	"context"
	"strings"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases/base/distro"
)

func renderProvisionScript(t *testing.T) string {
	t.Helper()
	s, err := getProvisionScript(context.Background(), ProvisionScriptParams{
		BusyBox:    "/usr/bin/busybox",
		ResultPath: "/provision.result",
		Provider:   "",
		Distro:     distro.NewTemplateData(),
	})
	if err != nil {
		t.Fatalf("rendering provision.sh: %v", err)
	}

	return s
}

// The script keys on the DECLARED distro id, never on which package-manager
// binary happens to exist.
func TestProvisionScriptNoPackageManagerProbing(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	for _, bad := range []string{
		"command -v apt-get", "command -v dnf", "command -v yum",
		"command -v microdnf", "command -v pacman", "PKG_FAMILY",
	} {
		if strings.Contains(script, bad) {
			t.Errorf("script leaked package-manager probing: %q", bad)
		}
	}
	if !strings.Contains(script, `case "$E2B_DISTRO_ID" in`) {
		t.Error("script must switch on $E2B_DISTRO_ID (declared distro identity)")
	}
	// A leftover action delimiter means something rendered as literal text.
	if strings.Contains(script, "{{") {
		t.Error("rendered script contains an unexecuted template action")
	}
}

// Every supported id gets a case arm; an unknown id hits the failing default.
func TestProvisionScriptCoversIDsAndRejects(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	for _, id := range distro.SupportedIDs() {
		if !strings.Contains(script, id) {
			t.Errorf("script missing arm for supported id %q", id)
		}
	}
	for _, want := range []string{"*)", "unsupported base image", "exit 1"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing fast-reject piece %q", want)
		}
	}
}

// Each profile reaches the guest through one template action per field, and
// the init-setup bodies now live in distro/init-*.sh rather than in Go. A
// dropped or misnamed action leaves the Go-side tests over Profiles and
// initSetup green while the rendered case arm never defines the variable
// provision.sh reads a few lines later — fatal under `set -u`, on every
// distro. So assert each arm carries the exact text the view produced.
func TestProvisionScriptSplicesEveryProfileField(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	if !strings.Contains(script, "e2b_init_setup() {") {
		t.Fatal("script must define e2b_init_setup()")
	}
	for _, p := range distro.NewTemplateData().Profiles {
		want := map[string]string{
			"E2B_PACKAGES":      "E2B_PACKAGES=" + p.Packages,
			"e2b_pkg_query":     "e2b_pkg_query() { " + p.PkgQuery + "; }",
			"e2b_pkg_install":   "e2b_pkg_install() { " + p.PkgInstall + "; }",
			"E2B_INIT_BIN":      "E2B_INIT_BIN=" + p.InitBinary,
			"E2B_TIMESYNC_UNIT": "E2B_TIMESYNC_UNIT=" + p.TimeSyncUnit,
			"E2B_SSH_UNIT":      "E2B_SSH_UNIT=" + p.SSHUnit,
			"E2B_ADMIN_GROUP":   "E2B_ADMIN_GROUP=" + p.AdminGroup,
			"E2B_CA_BUNDLE":     "E2B_CA_BUNDLE=" + p.CABundle,
			"e2b_ca_refresh":    "e2b_ca_refresh() { " + p.CARefresh + "; }",
			"E2B_INIT_SYSTEM":   "E2B_INIT_SYSTEM=" + p.InitSystem,
			"init setup body":   p.InitSetup,
		}
		if p.Bootstrap != "" {
			want["Bootstrap"] = p.Bootstrap
		}
		for field, text := range want {
			if !strings.Contains(script, text) {
				t.Errorf("profile %q: %s not spliced into the rendered script (wanted %q)", p.Key, field, text)
			}
		}
	}
}

// Provisioning must not decide the chrony time source: it runs on a build node,
// and the sandbox can cold-boot on a node with a different PHC situation. The
// baked config only includes what e2b-chrony-source writes at boot. Asserted on
// the rendered script, not the raw template: the init-setup blocks moved out to
// distro/init-*.sh, so only the rendered form covers both halves.
func TestProvisionScriptDefersChronySourceToBoot(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	// E2B_CHRONY_PHC was the provision-time verdict the Alpine seccomp workaround
	// used to read. It no longer exists, and under `set -u` a leftover reference
	// is a hard provisioning failure on every distro, not just Alpine.
	for _, bad := range []string{"[ -e /dev/ptp0 ]", "refclock PHC", "E2B_CHRONY_PHC"} {
		if strings.Contains(script, bad) {
			t.Errorf("provision.sh must not decide the time source (%q) — that happens at boot", bad)
		}
	}
	for _, want := range []string{
		`echo "include /run/chrony-e2b/source.conf"`,
		`echo "makestep 1.0 3"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("provision.sh missing chrony config line %q", want)
		}
	}
}

// An id we don't know falls back to ID_LIKE with a warning instead of failing
// (Kali declares ID=kali ID_LIKE=debian); nothing matching is still fatal.
func TestProvisionScriptIDLikeFallback(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	if !strings.Contains(script, "e2b_select_profile") {
		t.Error("selection must be a function so it can be retried per ID_LIKE token")
	}
	if !strings.Contains(script, "for e2b_like in $E2B_ID_LIKE; do") {
		t.Error("script must retry each ID_LIKE token")
	}
	if !strings.Contains(script, "WARNING") {
		t.Error("an ID_LIKE match must warn, not pass silently")
	}
	if !strings.Contains(script, "unsupported base image distribution") {
		t.Error("an id matching neither ID nor ID_LIKE must still fail")
	}
	// An if-condition call runs the function body with errexit suppressed.
	if strings.Contains(script, "if e2b_select_profile") || strings.Contains(script, "if ! e2b_select_profile") {
		t.Error("e2b_select_profile must not be invoked as an if-condition (errexit suppression)")
	}
	if !strings.Contains(script, "e2b_profile_matched=1") {
		t.Error("a matched profile arm must set e2b_profile_matched")
	}
}

// ID_LIKE must not re-admit the ids the rhel profile documents as out of
// scope: Oracle and Amazon Linux both declare ID_LIKE=fedora, so the guard
// must run before the fallback loop.
func TestProvisionScriptRejectedIDsGuard(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	guard := strings.Join(distro.RejectedIDs, "|")
	if !strings.Contains(script, guard) {
		t.Errorf("script must guard rejected ids (%s) before the ID_LIKE fallback", guard)
	}
	// Anchor on the loop line: E2B_ID_LIKE itself is first assigned in the
	// os-release detection block above the guard.
	if strings.Index(script, guard) > strings.Index(script, "for e2b_like in $E2B_ID_LIKE") {
		t.Error("the rejected-id guard must precede the ID_LIKE fallback loop")
	}
}

// Byte-exact customer-visible messages: the integration test
// TestTemplateBuildUnsupportedDistro (and customer log greps) pin these.
func TestProvisionScriptCustomerMessages(t *testing.T) {
	t.Parallel()
	script := renderProvisionScript(t)
	for _, want := range []string{
		`[provision] ERROR: base image distribution ID='$E2B_DISTRO_ID' is not supported.`,
		`[provision] Sandboxes boot E2B's kernel, so the kABI, signed modules and SELinux these images are chosen for are unavailable.`,
		`[provision] ERROR: unsupported base image distribution: ID='${E2B_DISTRO_ID:-unknown}'.`,
		"[provision] E2B template builds support: " + strings.Join(distro.SupportedIDs(), ", ") + ".",
		`[provision] WARNING: base image distribution ID='$E2B_DISTRO_ID' is not officially supported; provisioning it as '$e2b_like_match' from ID_LIKE. This is best effort and untested.`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing customer-visible message %q", want)
		}
	}
}
