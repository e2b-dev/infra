package featureflags

import (
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
)

func newResolverTestClient(t *testing.T, versions map[string]string) *Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(FirecrackerVersions.Key()).ValueForAll(ldvalue.FromJSONMarshal(versions)))

	client, err := NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(t.Context()))
	})

	return client
}

func TestResolveFirecrackerVersion_LegacyGoldens(t *testing.T) {
	t.Parallel()

	// Pins key derivation and fallback semantics for the legacy versions stored in production.
	client := newResolverTestClient(t, map[string]string{
		"v1.10": "mapped-v1.10",
		"v1.12": "mapped-v1.12",
		"v1.14": "mapped-v1.14",
	})

	cases := []struct {
		stored string
		want   string
	}{
		{"v1.10.1_30cbb07", "mapped-v1.10"},
		{"v1.12.1_210cbac", "mapped-v1.12"},
		{"v1.14.1_431f1fc", "mapped-v1.14"},
		{"v1.14.1", "v1.14.1"},
		{"v9.9.9_abcdefg", "v9.9.9_abcdefg"},
		{"not-a-version", "not-a-version"},
	}

	for _, tc := range cases {
		t.Run(tc.stored, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ResolveFirecrackerVersion(t.Context(), client, tc.stored))
		})
	}
}

// Guards the default map shipped in this package: every entry's key must be
// the LD key its value derives to, or the lookup silently never hits.
func TestFirecrackerVersionMap_KeysMatchLDKeys(t *testing.T) {
	t.Parallel()

	for key, value := range FirecrackerVersionMap {
		info, err := fcversion.New(value)
		require.NoError(t, err)

		derived, ok := info.LDKey()
		require.True(t, ok)
		assert.Equal(t, key, derived)
	}
}

func TestResolveFirecrackerVersion_MixedFormatMap(t *testing.T) {
	t.Parallel()

	// Legacy and e2b-format lines coexist in the map: each stored format
	// resolves through its own key, so the map can never remap across the
	// compatibility contract.
	client := newResolverTestClient(t, map[string]string{
		"v1.14":   "v1.14.1_431f1fc",
		"v1.14-0": "v1.14-0.2.0",
	})

	cases := []struct {
		stored string
		want   string
	}{
		{"v1.14.1_431f1fc", "v1.14.1_431f1fc"},
		{"v1.14-0.1.0", "v1.14-0.2.0"},
		{"v1.14-1.0.0", "v1.14-1.0.0"},
	}

	for _, tc := range cases {
		t.Run(tc.stored, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ResolveFirecrackerVersion(t.Context(), client, tc.stored))
		})
	}
}
