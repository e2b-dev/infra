//go:build linux

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
)

// TestEnvdUpgradeDeclineReason pins the gate that keeps a live upgrade from replacing an envd
// process whose state the orchestrator could not restore afterwards.
//
// It matters because its failure mode is silent in both directions. Let a swap through with
// nothing to re-send and the sandbox serves a value nobody chose for the rest of its life,
// with a 200 on every RPC. Decline one that could have proceeded and the sandbox simply never
// upgrades, which nothing else reports. So the policy is asserted here rather than only at its
// call site, and both reasons are asserted as literals, since they are dashboard values.
func TestEnvdUpgradeDeclineReason(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		sentUser            string
		workdirWithheld     bool
		envdReportsDefaults bool
		want                string
	}{
		// A user was established and sent, so the post-upgrade /init carries it too.
		"user established, nothing withheld": {sentUser: "user", want: ""},
		"any established user proceeds":      {sentUser: "app", want: ""},

		// Nothing was sent AND the running envd is not holding one, so nothing would be
		// re-sent after the swap and nothing would carry it across. Every template whose
		// metadata records a name other than the one provable value lands here while its
		// envd predates the header, including one genuinely serving that name.
		"nothing established, envd is silent": {sentUser: "", want: "no_default_user"},

		// The capability case for the USER, and the reason this rule retires itself too.
		// The host has nothing to send, but the running envd reports a delivered identity,
		// so its handover blob restores that identity across the execve and the empty
		// post-upgrade /init is a no-op rather than an overwrite. Declining here would
		// exclude the template for as long as it stays a memory snapshot, with no end date.
		"nothing established but envd carries it": {
			sentUser: "", envdReportsDefaults: true, want: "",
		},
		"nothing established, envd carries both": {
			sentUser: "", workdirWithheld: true, envdReportsDefaults: true, want: "",
		},

		// The identity is the more severe loss, so it is reported even when a workdir is
		// also at stake. A dashboard splitting the two must not double-count.
		"no user outranks a withheld workdir": {
			sentUser: "", workdirWithheld: true, want: "no_default_user",
		},

		// A recorded workdir that was not re-sent survives only in the process being
		// replaced, and the metadata cannot establish it, so the swap would drop it for
		// good.
		"withheld workdir and envd cannot carry it": {
			sentUser: "user", workdirWithheld: true, envdReportsDefaults: false,
			want: "unprovable_workdir",
		},

		// The capability case, and the reason the rule retires itself: an envd that
		// reports its effective defaults also carries them across the handover, so the
		// workdir survives without the host re-sending anything.
		"withheld workdir but envd carries it": {
			sentUser: "user", workdirWithheld: true, envdReportsDefaults: true, want: "",
		},

		// Capability alone must not admit or decline anything — with no workdir at risk
		// both answers are the same.
		"no workdir at risk, envd reports":   {sentUser: "user", envdReportsDefaults: true, want: ""},
		"no workdir at risk, envd is silent": {sentUser: "user", envdReportsDefaults: false, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want,
				envdUpgradeDeclineReason(tc.sentUser, tc.workdirWithheld, tc.envdReportsDefaults))
		})
	}

	// The literals, not the constants: asserting through the constants would move with a
	// rename and pin nothing. These strings appear on dashboards and in the rollout gate.
	assert.Equal(t, "no_default_user", envdUpgradeGateNoDefaultUser)
	assert.Equal(t, "unprovable_workdir", envdUpgradeGateUnprovableWorkdir)
}

// TestEnvdPreservesExecContext pins the capability test the workdir rule keys on.
//
// The second term is the one that looks redundant and is not: an envd that reports a header
// but was never told a user writes no defaults into the handover blob at all, so its blob
// preserves the workdir no better than a silent envd's. Checking only for a reported header
// would admit exactly that swap.
func TestEnvdPreservesExecContext(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		reported *sandbox.EnvdEffectiveDefaults
		want     bool
	}{
		"never reported anything":            {reported: nil, want: false},
		"reported a delivered context":       {reported: &sandbox.EnvdEffectiveDefaults{User: "user"}, want: true},
		"reported, but on the fallback":      {reported: &sandbox.EnvdEffectiveDefaults{User: "root", Fallback: true}, want: false},
		"delivered user that IS the builtin": {reported: &sandbox.EnvdEffectiveDefaults{User: "root"}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, envdPreservesExecContext(tc.reported))
		})
	}
}
