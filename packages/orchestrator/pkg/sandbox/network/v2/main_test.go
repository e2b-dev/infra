//go:build linux

package v2

import (
	"fmt"
	"os"
	"testing"
)

// The host firewall table is a host-wide singleton, so elements left by a
// crashed run would fail the emptiness assertions of a later one. Reconcile it
// to empty once per process — the same call the orchestrator makes at startup.
func TestMain(m *testing.M) {
	if os.Geteuid() == 0 {
		if err := reconcileHostFirewall(); err != nil {
			panic(fmt.Sprintf("cannot reset the host firewall table: %v", err))
		}
	}

	m.Run()
}

func reconcileHostFirewall() error {
	hf, err := NewHostFirewall("lo", testConfig())
	if err != nil {
		return err
	}

	if err := hf.ReconcileSlots(nil); err != nil {
		return err
	}

	return hf.Close()
}
