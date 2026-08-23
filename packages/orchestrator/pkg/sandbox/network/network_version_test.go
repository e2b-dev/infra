//go:build linux

package network

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// An absent NETWORK_VERSION must select v1: the v2 datapath is opt-in.
func TestParseConfig_NetworkVersion(t *testing.T) {
	t.Run("absent selects v1", func(t *testing.T) {
		t.Setenv("NETWORK_VERSION", "")
		require.NoError(t, os.Unsetenv("NETWORK_VERSION"))

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 1, config.NetworkVersion)
	})

	t.Run("explicit 1 selects v1", func(t *testing.T) {
		t.Setenv("NETWORK_VERSION", "1")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 1, config.NetworkVersion)
	})

	t.Run("set but empty selects v1", func(t *testing.T) {
		t.Setenv("NETWORK_VERSION", "")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 1, config.NetworkVersion)
	})

	t.Run("explicit 2 selects v2", func(t *testing.T) {
		t.Setenv("NETWORK_VERSION", "2")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 2, config.NetworkVersion)
	})

	t.Run("non-numeric fails loudly", func(t *testing.T) {
		t.Setenv("NETWORK_VERSION", "two")

		_, err := ParseConfig()
		require.Error(t, err)
	})

	for _, invalid := range []string{"0", "-1", "3"} {
		t.Run("unsupported "+invalid+" fails loudly", func(t *testing.T) {
			t.Setenv("NETWORK_VERSION", invalid)

			_, err := ParseConfig()
			require.ErrorContains(t, err, "NETWORK_VERSION="+invalid+" unsupported")
		})
	}
}

func TestConfig_ValidateNetworkVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []int{1, 2} {
		require.NoError(t, (Config{NetworkVersion: version}).Validate())
	}
	for _, version := range []int{0, -1, 3} {
		require.Error(t, (Config{NetworkVersion: version}).Validate())
	}
}
