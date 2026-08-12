//go:build linux

package v2

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
)

// createTestVeth brings up a real veth pair and returns its host-side name;
// Attach and ReadCounters resolve the interface through netlink, so a name
// with no link behind it fails. The name is unique per call so a concurrent
// package, a repeat run, or a live orchestrator cannot collide with it.
func createTestVeth(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("vt-%d", reserveNSTestIdx(t))
	link := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: name},
		PeerName:  name + "-peer",
	}

	require.NoError(t, netlink.LinkAdd(link))
	t.Cleanup(func() {
		_ = netlink.LinkDel(link)
	})

	return name
}

// sendTestVethTraffic pushes packets through the veth pair so its link
// counters advance.
func sendTestVethTraffic(t *testing.T, name string) {
	t.Helper()

	peer, err := netlink.LinkByName(name + "-peer")
	require.NoError(t, err)
	require.NoError(t, netlink.LinkSetUp(peer))

	link, err := netlink.LinkByName(name)
	require.NoError(t, err)
	require.NoError(t, netlink.LinkSetUp(link))

	// An unanswered ARP for an address on the peer's subnet is enough to move
	// the counters, and needs no addressing on either end.
	addr, err := netlink.ParseAddr("169.254.222.1/30")
	require.NoError(t, err)
	require.NoError(t, netlink.AddrAdd(link, addr))

	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(t.Context(), "udp4", "169.254.222.2:9")
	require.NoError(t, err)
	defer conn.Close()

	for range 4 {
		_, _ = conn.Write([]byte("counter-probe"))
	}
}

func TestVethObserver_AttachDetach(t *testing.T) {
	t.Parallel()

	veth := createTestVeth(t)

	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Attach
	err = obs.Attach(veth)
	require.NoError(t, err)

	// Double attach should fail
	err = obs.Attach(veth)
	require.Error(t, err)

	// Detach
	err = obs.Detach(veth)
	require.NoError(t, err)

	// Double detach is idempotent
	err = obs.Detach(veth)
	assert.NoError(t, err)
}

func TestVethObserver_ReadCounters(t *testing.T) {
	t.Parallel()

	veth := createTestVeth(t)

	obs, err := NewVethObserver()
	require.NoError(t, err)
	defer obs.Close()

	// Not attached → error
	_, _, err = obs.ReadCounters(veth)
	require.Error(t, err)

	// Attach and read
	require.NoError(t, obs.Attach(veth))
	packets, bytes, err := obs.ReadCounters(veth)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), packets)
	assert.Equal(t, uint64(0), bytes)

	// Counters that never move would satisfy the assertions above, so make
	// the link carry something and read again.
	sendTestVethTraffic(t, veth)

	packets, bytes, err = obs.ReadCounters(veth)
	require.NoError(t, err)
	assert.Positive(t, packets, "counters must track real traffic")
	assert.Positive(t, bytes, "counters must track real traffic")
}

func TestVethObserver_NilSafe(t *testing.T) {
	t.Parallel()

	var obs *VethObserver

	// A nil observer returns before touching netlink, so the name need not exist.
	assert.NoError(t, obs.Attach("vt-absent"))
	assert.NoError(t, obs.Detach("vt-absent"))
	assert.NoError(t, obs.Close())

	p, b, err := obs.ReadCounters("vt-absent")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), p)
	assert.Equal(t, uint64(0), b)
}

func TestVethObserver_Close(t *testing.T) {
	t.Parallel()

	veth1 := createTestVeth(t)
	veth2 := createTestVeth(t)

	obs, err := NewVethObserver()
	require.NoError(t, err)

	require.NoError(t, obs.Attach(veth1))
	require.NoError(t, obs.Attach(veth2))

	err = obs.Close()
	require.NoError(t, err)

	// After close, nothing should be attached
	obs.mu.Lock()
	assert.Empty(t, obs.attached)
	obs.mu.Unlock()
}
