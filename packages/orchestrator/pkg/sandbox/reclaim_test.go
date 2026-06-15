//go:build linux

package sandbox

import (
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestRamScaledSyncTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ramMB int64
		want  time.Duration
	}{
		// 128 MiB / 50 MiB/s ≈ 2.5s -> clamped up to the floor.
		{"small RAM clamps to min", 128, syncMinTimeout},
		// 1024 MiB / 50 MiB/s ≈ 20s.
		{"scales with RAM", 1024, 20 * time.Second},
		// 128 GiB / 50 MiB/s ≈ 2621s -> clamped down to the cap.
		{"large RAM clamps to max", 128 * 1024, syncMaxTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ramScaledSyncTimeout(tt.ramMB))
		})
	}
}

func newSandboxWithFF(t *testing.T, ramMB int64, td *ldtestdata.TestDataSource) *Sandbox {
	t.Helper()

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(t.Context()) })

	s := &Sandbox{Metadata: &Metadata{Config: &Config{RamMB: ramMB}}}
	s.featureFlags = ff

	return s
}

func TestGuestSyncTimeout_FlagOverride(t *testing.T) {
	t.Parallel()

	t.Run("positive flag pins the timeout regardless of RAM", func(t *testing.T) {
		t.Parallel()
		td := ldtestdata.DataSource()
		td.Update(td.Flag(featureflags.GuestSyncTimeoutMs.Key()).ValueForAll(ldvalue.Int(30000)))
		s := newSandboxWithFF(t, 1024, td) // RAM-derived would be 20s

		assert.Equal(t, 30*time.Second, s.guestSyncTimeout(t.Context()))
	})

	t.Run("unset flag falls back to RAM-derived", func(t *testing.T) {
		t.Parallel()
		s := newSandboxWithFF(t, 1024, ldtestdata.DataSource())

		assert.Equal(t, ramScaledSyncTimeout(1024), s.guestSyncTimeout(t.Context()))
	})
}
