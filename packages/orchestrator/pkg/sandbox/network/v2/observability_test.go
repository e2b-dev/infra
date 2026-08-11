//go:build linux

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

// createTestVeth brings up a real veth pair; Attach and ReadCounters resolve
// the interface through netlink, so a name with no link behind it fails.
func createTestVeth(t *testing.T, name string) {
	t.Helper()

	link := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: name},
		PeerName:  name + "-peer",
	}

	require.NoError(t, netlink.LinkAdd(link))
	t.Cleanup(func() {
		_ = netlink.LinkDel(link)
	})
}

func TestVethObserver_AttachDetach(t *testing.T) { //nolint:paralleltest // creates the fixed-name host veth link "veth-1" shared across VethObserver tests
	createTestVeth(t, "veth-1")

	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Attach
	err = obs.Attach("veth-1")
	require.NoError(t, err)

	// Double attach should fail
	err = obs.Attach("veth-1")
	require.Error(t, err)

	// Detach
	err = obs.Detach("veth-1")
	require.NoError(t, err)

	// Double detach is idempotent
	err = obs.Detach("veth-1")
	assert.NoError(t, err)
}

func TestVethObserver_ReadCounters(t *testing.T) { //nolint:paralleltest // creates the fixed-name host veth link "veth-1" shared across VethObserver tests
	createTestVeth(t, "veth-1")

	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Not attached → error
	_, _, err = obs.ReadCounters("veth-1")
	require.Error(t, err)

	// Attach and read
	require.NoError(t, obs.Attach("veth-1"))
	packets, bytes, err := obs.ReadCounters("veth-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), packets)
	assert.Equal(t, uint64(0), bytes)
}

func TestVethObserver_NilSafe(t *testing.T) {
	t.Parallel()

	var obs *VethObserver

	assert.NoError(t, obs.Attach("veth-1"))
	assert.NoError(t, obs.Detach("veth-1"))
	assert.NoError(t, obs.Close())

	p, b, err := obs.ReadCounters("veth-1")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), p)
	assert.Equal(t, uint64(0), b)
}

func TestVethObserver_Close(t *testing.T) { //nolint:paralleltest // creates the fixed-name host veth links "veth-1"/"veth-2" shared across VethObserver tests
	createTestVeth(t, "veth-1")
	createTestVeth(t, "veth-2")

	obs, err := NewVethObserver()
	require.NoError(t, err)

	require.NoError(t, obs.Attach("veth-1"))
	require.NoError(t, obs.Attach("veth-2"))

	err = obs.Close()
	require.NoError(t, err)

	// After close, nothing should be attached
	obs.mu.Lock()
	assert.Empty(t, obs.attached)
	obs.mu.Unlock()
}
