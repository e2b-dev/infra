package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

// TestResolveEnvdDefaultUser pins both halves of one decision: which user a resume sends,
// and the label envdDefaultsApplied counts it under. They come from one function so they
// cannot disagree — the metric is the only signal separating "the derivation ran and
// produced a value" from "the counter is absent", which render identically on a dashboard,
// so a source value that describes a different population than the code took is invisible.
//
// The "inherited" row is the reachable case that is easy to miss: ResumeSandbox writes its
// result back into the config it was handed, and the checkpoint path hands that same config
// to the sandbox's next resume, which therefore finds a user already in place.
func TestResolveEnvdDefaultUser(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		meta       metadata.Template
		configured *string
		wantUser   *string
		wantSource string
	}{
		"the one establishable user": {
			meta:       metadata.Template{Context: metadata.Context{User: "user"}},
			wantUser:   new("user"),
			wantSource: "metadata",
		},
		"a recorded root is indeterminate": {
			meta:       metadata.Template{Context: metadata.Context{User: "root"}},
			wantUser:   nil,
			wantSource: "indeterminate",
		},
		"a named user is indeterminate": {
			meta:       metadata.Template{Context: metadata.Context{User: "app"}},
			wantUser:   nil,
			wantSource: "indeterminate",
		},
		"no recorded user is indeterminate": {
			meta:       metadata.Template{},
			wantUser:   nil,
			wantSource: "indeterminate",
		},
		"a configured user is sent unchanged and counted as inherited": {
			meta:       metadata.Template{Context: metadata.Context{User: "user"}},
			configured: new("app"),
			wantUser:   new("app"),
			wantSource: "inherited",
		},
		"a configured user wins over metadata that establishes nothing": {
			meta:       metadata.Template{},
			configured: new("user"),
			wantUser:   new("user"),
			wantSource: "inherited",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			user, source := resolveEnvdDefaultUser(tc.meta, tc.configured)

			assert.Equal(t, tc.wantSource, source)
			if tc.wantUser == nil {
				assert.Nil(t, user)
			} else if assert.NotNil(t, user) {
				assert.Equal(t, *tc.wantUser, *user)
			}
		})
	}

	// The literals, not the constants: asserting through the constants would move with a
	// rename and pin nothing. These strings appear in dashboards and in the rollout gate.
	assert.Equal(t, "metadata", defaultsSourceMetadata)
	assert.Equal(t, "indeterminate", defaultsSourceIndeterminate)
	assert.Equal(t, "inherited", defaultsSourceInherited)
}
