//go:build linux

package network

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An absent NETWORK_VERSION must select v1: the v2 datapath is opt-in.
func TestParseConfig_NetworkVersion(t *testing.T) { //nolint:paralleltest // t.Setenv
	t.Run("absent selects v1", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 1, config.NetworkVersion)
	})

	t.Run("set but empty selects v1", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("NETWORK_VERSION", "")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 1, config.NetworkVersion)
	})

	t.Run("explicit 2 selects v2", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("NETWORK_VERSION", "2")

		config, err := ParseConfig()
		require.NoError(t, err)
		require.Equal(t, 2, config.NetworkVersion)
	})

	t.Run("non-numeric fails loudly", func(t *testing.T) { //nolint:paralleltest // t.Setenv
		t.Setenv("NETWORK_VERSION", "two")

		_, err := ParseConfig()
		require.Error(t, err)
	})
}
