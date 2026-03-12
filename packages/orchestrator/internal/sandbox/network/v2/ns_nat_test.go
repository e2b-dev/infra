package v2

import (
	"os"
	"runtime"
	"testing"

	"github.com/google/nftables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNotLinuxRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("skipping: requires linux")
	}
	if os.Getuid() != 0 {
		t.Skip("skipping: requires root")
	}
}

func TestNamespaceNAT_Setup(t *testing.T) {
	skipIfNotLinuxRoot(t)

	conn, err := nftables.New(nftables.AsLasting())
	require.NoError(t, err)
	defer conn.CloseLasting()

	table := conn.AddTable(&nftables.Table{
		Name:   "test-nat",
		Family: nftables.TableFamilyINet,
	})
	require.NoError(t, conn.Flush())
	defer func() {
		conn.DelTable(table)
		conn.Flush()
	}()

	err = SetupNamespaceNAT(conn, table, "eth0", "10.11.0.1", "169.254.0.21")
	assert.NoError(t, err)

	// Verify chains were created
	chains, err := conn.ListChainsOfTableFamily(nftables.TableFamilyINet)
	require.NoError(t, err)

	chainNames := make(map[string]bool)
	for _, c := range chains {
		if c.Table.Name == "test-nat" {
			chainNames[c.Name] = true
		}
	}
	assert.True(t, chainNames["postroute_nat"], "postroute_nat chain should exist")
	assert.True(t, chainNames["preroute_nat"], "preroute_nat chain should exist")
}

func TestNamespaceNAT_InvalidIP(t *testing.T) {
	skipIfNotLinuxRoot(t)

	conn, err := nftables.New(nftables.AsLasting())
	require.NoError(t, err)
	defer conn.CloseLasting()

	table := conn.AddTable(&nftables.Table{
		Name:   "test-nat-invalid",
		Family: nftables.TableFamilyINet,
	})
	require.NoError(t, conn.Flush())
	defer func() {
		conn.DelTable(table)
		conn.Flush()
	}()

	err = SetupNamespaceNAT(conn, table, "eth0", "not-an-ip", "169.254.0.21")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid host IP")
}

func TestIfnameBytes(t *testing.T) {
	b := ifnameBytes("eth0")
	assert.Len(t, b, 16)
	assert.Equal(t, byte('e'), b[0])
	assert.Equal(t, byte('t'), b[1])
	assert.Equal(t, byte('h'), b[2])
	assert.Equal(t, byte('0'), b[3])
	assert.Equal(t, byte(0), b[4]) // null terminated
}
