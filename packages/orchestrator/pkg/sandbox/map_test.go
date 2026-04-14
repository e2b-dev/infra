package sandbox

import (
	"net"
	"testing"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

func TestGetByHostPortPrefersRunningSandbox(t *testing.T) {
	m := NewSandboxesMap()

	stopping := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.2")},
		},
	}
	stopping.status.Store(int32(StatusStopping))
	m.sandboxes.Insert("stopping", stopping)

	running := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.2")},
		},
	}
	running.status.Store(int32(StatusRunning))
	m.sandboxes.Insert("running", running)

	sbx, err := m.GetByHostPort("10.11.0.2:2049")
	if err != nil {
		t.Fatalf("GetByHostPort returned error: %v", err)
	}

	if sbx != running {
		t.Fatalf("expected running sandbox, got %#v", sbx)
	}
}

func TestGetByHostPortFallsBackToStoppingSandbox(t *testing.T) {
	m := NewSandboxesMap()

	stopping := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.3")},
		},
	}
	stopping.status.Store(int32(StatusStopping))
	m.sandboxes.Insert("stopping", stopping)

	sbx, err := m.GetByHostPort("10.11.0.3:2049")
	if err != nil {
		t.Fatalf("GetByHostPort returned error: %v", err)
	}

	if sbx != stopping {
		t.Fatalf("expected stopping sandbox, got %#v", sbx)
	}
}
