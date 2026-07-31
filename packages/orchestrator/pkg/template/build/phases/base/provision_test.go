//go:build linux

package base

import (
	"strings"
	"testing"
)

// Provisioning must not decide the chrony time source: it runs on a build node,
// and the sandbox can cold-boot on a node with a different PHC situation. The
// baked config only includes what e2b-chrony-source writes at boot.
func TestProvisionScriptDefersChronySourceToBoot(t *testing.T) {
	t.Parallel()
	// E2B_CHRONY_PHC was the provision-time verdict the Alpine seccomp workaround
	// used to read. It no longer exists, and under `set -u` a leftover reference
	// is a hard provisioning failure on every distro, not just Alpine.
	for _, bad := range []string{"[ -e /dev/ptp0 ]", "refclock PHC", "E2B_CHRONY_PHC"} {
		if strings.Contains(provisionScriptFile, bad) {
			t.Errorf("provision.sh must not decide the time source (%q) — that happens at boot", bad)
		}
	}
	for _, want := range []string{
		`echo "include /run/chrony-e2b/source.conf"`,
		`echo "makestep 1.0 3"`,
	} {
		if !strings.Contains(provisionScriptFile, want) {
			t.Errorf("provision.sh missing chrony config line %q", want)
		}
	}
}
