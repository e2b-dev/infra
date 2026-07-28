package api

import (
	"github.com/e2b-dev/infra/packages/envd/internal/services/spec/upgrade"
)

// ExportMounts snapshots the NFS mount ledger (path -> lifecycle id) as typed
// MountEntry messages, carried across an envd live-upgrade. The kernel mounts
// survive the same-PID execve, but the ledger lives only in envd's heap; without
// it the new envd's post-upgrade /init would see an empty ledger, decide every
// volume needs (re)mounting, and force-unmount + remount a still-live mount —
// risking ESTALE for the workload and a failed resume. Carrying it lets /init
// recognize a matching-lifecycle mount and leave it in place.
func (a *API) ExportMounts() []*upgrade.MountEntry {
	out := make([]*upgrade.MountEntry, 0)
	a.mountedPaths.Range(func(k, v any) bool {
		path, _ := k.(string)
		lifecycle, _ := v.(string)
		out = append(out, &upgrade.MountEntry{Path: path, LifecycleId: lifecycle})

		return true
	})

	return out
}

// ImportMounts restores the NFS mount ledger from a live-upgrade handover so the
// new envd knows which paths are already mounted (and for which lifecycle)
// before its first post-upgrade /init runs setupNFS.
func (a *API) ImportMounts(mounts []*upgrade.MountEntry) {
	for _, m := range mounts {
		a.mountedPaths.Store(m.GetPath(), m.GetLifecycleId())
	}
}
