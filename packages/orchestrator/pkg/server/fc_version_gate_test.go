//go:build linux

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/shared/pkg/fcversion"
)

// TestFirecrackerSupports pins the server-side version gate for the E2B
// snapshot feature set: only e2b-format releases >= 0.2.0 qualify; legacy
// _hash builds — including dev builds that mechanically carry the endpoints —
// and unparsable versions fail CLOSED. The gate feeds two call sites with
// different failure modes (in-place checkpoint falls back to resume-fresh;
// a filesystem-only pause is refused), so it must never err toward true.
func TestFirecrackerSupports(t *testing.T) {
	t.Parallel()

	mkSbx := func(version string) *sandbox.Sandbox {
		return &sandbox.Sandbox{Metadata: &sandbox.Metadata{
			Config: sandbox.NewConfig(sandbox.Config{
				FirecrackerConfig: fc.Config{FirecrackerVersion: version},
			}),
			Runtime: sandbox.RuntimeMetadata{SandboxID: "test-sbx"},
		}}
	}

	cases := []struct {
		version string
		inPlace bool
		fsOnly  bool
	}{
		{"v1.14-0.2.0", true, true},
		{"v1.14-1.0.0", true, true},
		{"v1.14-0.1.1", false, true},      // fs-only ships from 0.1.0; in-place needs the 0.2.0 balloon API
		{"v1.14.1_6ecb627", false, false}, // legacy build carrying the endpoints: still refused
		{"garbage", false, false},         // unparsable: fail closed
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()

			sbx := mkSbx(tc.version)
			assert.Equal(t, tc.inPlace,
				firecrackerSupports(t.Context(), sbx, "in-place checkpoint", (*fcversion.Info).HasInPlaceCheckpoint))
			assert.Equal(t, tc.fsOnly,
				firecrackerSupports(t.Context(), sbx, "filesystem-only snapshot", (*fcversion.Info).HasFilesystemSnapshots))
		})
	}
}
