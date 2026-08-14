//go:build linux

package v2

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

// TEST-NET-3: routed via the default gateway, never delivered anywhere.
var forwardProbeIP = net.IPv4(203, 0, 113, 9).To4()

const forwardProbePort = 48766

// setForwardPolicy sets the iptables filter FORWARD policy for this test and
// restores the original afterwards.
func setForwardPolicy(t *testing.T, tables *iptables.IPTables, policy string) {
	t.Helper()

	rules, err := tables.List("filter", "FORWARD")
	require.NoError(t, err)
	require.NotEmpty(t, rules)
	first := strings.Fields(rules[0])
	require.Lenf(t, first, 3, "unexpected FORWARD policy line: %q", rules[0])
	orig := first[2]

	if orig == policy {
		return
	}

	require.NoError(t, tables.ChangePolicy("filter", "FORWARD", policy))
	t.Cleanup(func() { _ = tables.ChangePolicy("filter", "FORWARD", orig) })
}

// probeForwarded sends a UDP probe from inside the slot's namespace toward an
// external address and reports whether it egresses on the host's gateway
// interface — a packet only gets there by surviving the forward hook, so this
// observes the FORWARD verdict itself.
func probeForwarded(t *testing.T, slot *network.Slot, gw string, timeout time.Duration) bool {
	t.Helper()

	link, err := netlink.LinkByName(gw)
	require.NoError(t, err)

	// ETH_P_ALL, not ETH_P_IP: the kernel delivers outgoing frames only to
	// all-protocol packet sockets, and the probe leaves this interface.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM, int(htons(unix.ETH_P_ALL)))
	require.NoError(t, err)
	defer unix.Close(fd)

	require.NoError(t, unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  link.Attrs().Index,
	}))
	require.NoError(t, unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}))

	sendFromNamespace(t, slot, forwardProbeIP, forwardProbePort)

	deadline := time.Now().Add(timeout)
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			continue
		}
		require.NoError(t, err)
		if n < 20 || buf[0]>>4 != 4 {
			continue
		}
		ihl := int(buf[0]&0x0f) * 4
		if buf[9] != unix.IPPROTO_UDP || n < ihl+8 {
			continue
		}
		if !net.IP(buf[12:16]).Equal(slot.VpeerIP().To4()) || !net.IP(buf[16:20]).Equal(forwardProbeIP) {
			continue
		}
		if int(buf[ihl+2])<<8|int(buf[ihl+3]) != forwardProbePort {
			continue
		}

		return true
	}

	return false
}

// The claim behind the per-slot iptables pair: with the host FORWARD policy at
// DROP (Docker's default), a v2 slot's egress still traverses the host exactly
// as v1's does — and deleting the pair, leaving only the v2 nftables accepts,
// blackholes it.
func TestCreateNetworkV2_ForwardsUnderDropPolicy(t *testing.T) { //nolint:paralleltest // mutates host netns state and the host FORWARD policy
	skipIfNotLinuxRoot(t)

	ctx := context.Background()

	tables, err := iptables.New()
	require.NoError(t, err)
	setForwardPolicy(t, tables, "DROP")

	gw := network.DefaultGateway()
	hf := newTestHostFirewallOn(t, gw, testConfig())

	slot := makeTestSlot(t, reserveNSTestIdx(t))
	sv2 := NewSlotV2(slot)
	require.NoError(t, CreateNetworkV2(ctx, slot, sv2, hf, nil))
	t.Cleanup(func() { _ = RemoveNetworkV2(ctx, slot, sv2, hf, nil) })

	// The delivered leg proves the capture rig, so the silent leg below means
	// dropped, not broken.
	require.Truef(t, probeForwarded(t, slot, gw, 10*time.Second),
		"probe must egress on %s with the accept pair installed", gw)

	for _, rule := range slotForwardAccepts(slot, gw) {
		require.NoError(t, tables.Delete("filter", "FORWARD", rule...))
	}

	require.False(t, probeForwarded(t, slot, gw, 3*time.Second),
		"without the accept pair the DROP policy must eat the probe")
}
