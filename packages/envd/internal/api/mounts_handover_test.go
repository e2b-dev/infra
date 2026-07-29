package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/upgrade"
)

// TestMountLedger_HandoverRoundTrip verifies the NFS mount ledger survives an
// Export -> Import cycle, so the new envd knows which paths were mounted (and for
// which lifecycle) after a live-upgrade.
func TestMountLedger_HandoverRoundTrip(t *testing.T) {
	t.Parallel()

	a := &API{}
	a.ImportMounts([]*upgrade.MountEntry{
		{Path: "/mnt/a", LifecycleId: "lc-1"},
		{Path: "/mnt/b", LifecycleId: "lc-2"},
	})

	got := map[string]string{}
	for _, m := range a.ExportMounts() {
		got[m.GetPath()] = m.GetLifecycleId()
	}
	assert.Equal(t, map[string]string{"/mnt/a": "lc-1", "/mnt/b": "lc-2"}, got)
}

// TestImportedMountLedger_SkipsRemountForSameLifecycle is the point of carrying
// the ledger: setupNFS consults it (mountedPaths.Load + shouldRemountNFS), and a
// path carried across the upgrade with an unchanged lifecycle must NOT be
// remounted — leaving the still-live kernel mount in place (no ESTALE). A changed
// lifecycle still triggers a remount.
func TestImportedMountLedger_SkipsRemountForSameLifecycle(t *testing.T) {
	t.Parallel()

	a := &API{}
	a.ImportMounts([]*upgrade.MountEntry{{Path: "/mnt/a", LifecycleId: "lc-1"}})

	v, ok := a.mountedPaths.Load("/mnt/a")
	require.True(t, ok, "carried mount must be present in the ledger")
	mountedLifecycle, _ := v.(string)

	assert.False(t, shouldRemountNFS(true, mountedLifecycle, "lc-1"),
		"a live mount carried across the upgrade must not be remounted for the same lifecycle")
	assert.True(t, shouldRemountNFS(true, mountedLifecycle, "lc-2"),
		"a lifecycle change must still trigger a remount")
}
