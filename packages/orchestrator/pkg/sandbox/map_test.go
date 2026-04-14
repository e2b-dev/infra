package sandbox

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

func TestGetByHostPortPrefersRunningSandbox(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, err)
	require.Same(t, running, sbx)
}

func TestGetByHostPortPrefersStartingOverStopping(t *testing.T) {
	t.Parallel()

	m := NewSandboxesMap()

	stopping := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.2")},
		},
	}
	stopping.status.Store(int32(StatusStopping))
	m.sandboxes.Insert("stopping", stopping)

	starting := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.2")},
		},
	}
	starting.status.Store(int32(StatusStarting))
	m.sandboxes.Insert("starting", starting)

	sbx, err := m.GetByHostPort("10.11.0.2:2049")
	require.NoError(t, err)
	require.Same(t, starting, sbx)
}

func TestGetByHostPortFallsBackToStoppingSandbox(t *testing.T) {
	t.Parallel()

	m := NewSandboxesMap()

	stopping := &Sandbox{
		Resources: &Resources{
			Slot: &network.Slot{HostIP: net.ParseIP("10.11.0.3")},
		},
	}
	stopping.status.Store(int32(StatusStopping))
	m.sandboxes.Insert("stopping", stopping)

	sbx, err := m.GetByHostPort("10.11.0.3:2049")
	require.NoError(t, err)
	require.Same(t, stopping, sbx)
}
