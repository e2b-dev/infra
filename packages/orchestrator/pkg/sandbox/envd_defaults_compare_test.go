package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvdDefaultsMismatches pins the skip rules, which are the half that can go wrong
// quietly: a comparison that fires when it should not floods the one counter the rollout
// gate reads, and one that stays silent when it should fire recreates the original
// invisibility.
func TestEnvdDefaultsMismatches(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		eff         EnvdEffectiveDefaults
		sentUser    string
		sentWorkdir string
		want        []string
	}{
		"agreement on both fields": {
			eff:         EnvdEffectiveDefaults{User: "user", Workdir: new("/opt/wd")},
			sentUser:    "user",
			sentWorkdir: "/opt/wd",
			want:        nil,
		},
		"the identity loss this exists to catch": {
			eff:      EnvdEffectiveDefaults{User: "root", Fallback: true},
			sentUser: "user",
			want:     []string{"user"},
		},
		"a workdir dropped across a handover": {
			eff:         EnvdEffectiveDefaults{User: "user"},
			sentUser:    "user",
			sentWorkdir: "/opt/wd",
			want:        []string{"workdir"},
		},
		"both fields lost": {
			eff:         EnvdEffectiveDefaults{User: "root", Fallback: true},
			sentUser:    "user",
			sentWorkdir: "/opt/wd",
			want:        []string{"user", "workdir"},
		},
		// A value we never sent cannot be violated. Without this, every start that sends
		// no workdir would report a mismatch against a value nobody asked for.
		"no workdir sent, envd holds one": {
			eff:      EnvdEffectiveDefaults{User: "user", Workdir: new("/somewhere")},
			sentUser: "user",
			want:     nil,
		},
		"no user sent is not a mismatch": {
			eff:  EnvdEffectiveDefaults{User: "root", Fallback: true},
			want: nil,
		},
		// envd omits the workdir when it holds none; the orchestrator sends "" for the
		// same state. Comparing representations rather than states would fire here.
		"unset on both sides is agreement": {
			eff:      EnvdEffectiveDefaults{User: "user"},
			sentUser: "user",
			want:     nil,
		},
		// A guest could return anything here. It must be reported, not trusted, and the
		// only label it influences is `field`, which is ours.
		"a hostile effective user is reported": {
			eff:      EnvdEffectiveDefaults{User: "../../etc/passwd"},
			sentUser: "user",
			want:     []string{"user"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, envdDefaultsMismatches(tc.eff, tc.sentUser, tc.sentWorkdir))
		})
	}
}

// TestDecodeEnvdEffectiveDefaults pins the bounds on a header the guest writes.
//
// The values are free-form strings from inside a VM the customer controls, and they reach a
// struct retained for the sandbox's lifetime, a span attribute on every /init and several log
// lines. Nothing else bounds them: the transport's own limit is 10 MiB and covers the whole
// header block, and no span attribute length limit is configured. So the cap is asserted at
// its boundary, in both directions, together with the positive case — an absence assertion
// alone would pass for a decoder that rejects everything.
func TestDecodeEnvdEffectiveDefaults(t *testing.T) {
	t.Parallel()

	// A header of exactly the cap, padded inside a legal JSON string.
	atCap := func(n int) string {
		head := `{"user":"`
		tail := `","fallback":false}`

		return head + strings.Repeat("u", n-len(head)-len(tail)) + tail
	}

	t.Run("an ordinary header decodes", func(t *testing.T) {
		t.Parallel()

		eff, err := decodeEnvdEffectiveDefaults(`{"user":"user","workdir":"/home/user","fallback":false}`)
		require.NoError(t, err)
		assert.Equal(t, "user", eff.User)
		require.NotNil(t, eff.Workdir)
		assert.Equal(t, "/home/user", *eff.Workdir)
		assert.False(t, eff.Fallback)
	})

	t.Run("exactly at the cap still decodes", func(t *testing.T) {
		t.Parallel()

		h := atCap(envdDefaultsHeaderMaxBytes)
		require.Len(t, h, envdDefaultsHeaderMaxBytes)
		_, err := decodeEnvdEffectiveDefaults(h)
		require.NoError(t, err)
	})

	t.Run("one byte over is refused", func(t *testing.T) {
		t.Parallel()

		h := atCap(envdDefaultsHeaderMaxBytes + 1)
		require.Len(t, h, envdDefaultsHeaderMaxBytes+1)
		_, err := decodeEnvdEffectiveDefaults(h)
		require.Error(t, err, "a header over the cap must not be parsed at all")
	})

	t.Run("a megabyte is refused", func(t *testing.T) {
		t.Parallel()

		_, err := decodeEnvdEffectiveDefaults(atCap(1 << 20))
		require.Error(t, err)
	})

	t.Run("decoding does not shorten the values", func(t *testing.T) {
		t.Parallel()

		// Deliberate: compareEnvdDefaults tests these against what the host sent, and
		// shortening one side of an equality would report a header that agreed as a
		// mismatch. The byte cap above is what bounds retention.
		long := strings.Repeat("u", envdDefaultsUserMaxLen*2)
		eff, err := decodeEnvdEffectiveDefaults(`{"user":"` + long + `","fallback":false}`)
		require.NoError(t, err)
		assert.Equal(t, long, eff.User)
	})

	t.Run("each field is bounded where it escapes", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("u", envdDefaultsUserMaxLen*2)
		wd := strings.Repeat("d", envdDefaultsWorkdirMaxLen*2)
		eff := EnvdEffectiveDefaults{User: long, Workdir: &wd}
		assert.Len(t, eff.effectiveUserForDisplay(), envdDefaultsUserMaxLen)
		assert.Len(t, eff.effectiveWorkdirForDisplay(), envdDefaultsWorkdirMaxLen)

		// The positive twin on the same artifact: a value inside the bound passes through
		// untouched, so the pair pins the length rather than the presence of a step.
		inside := EnvdEffectiveDefaults{User: "app", Workdir: new("/opt/wd")}
		assert.Equal(t, "app", inside.effectiveUserForDisplay())
		assert.Equal(t, "/opt/wd", inside.effectiveWorkdirForDisplay())

		// An absent workdir reads as empty, not as a truncated something.
		assert.Empty(t, EnvdEffectiveDefaults{}.effectiveWorkdirForDisplay())
	})

	t.Run("malformed json is refused", func(t *testing.T) {
		t.Parallel()

		_, err := decodeEnvdEffectiveDefaults(`{"user":`)
		require.Error(t, err)
	})
}
