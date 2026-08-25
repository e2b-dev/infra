package fcgate

import (
	"context"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// flagsWithVersionMap builds a real flags client whose firecracker-versions
// map is deliberately adversarial: it would CHANGE every answer below if
// consulted. The exact path must ignore it; the declared path must apply it
// exactly once.
func flagsWithVersionMap(t *testing.T, versions map[string]string) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.FirecrackerVersions.Key()).ValueForAll(ldvalue.FromJSONMarshal(versions)))

	client, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, client.Close(context.WithoutCancel(t.Context())))
	})

	return client
}

// TestSupportsFilesystemSnapshots_ExactNeverResolves pins the property the
// live-VM gates rest on: the exact check answers from the version string
// alone. The flag map in scope flips every answer (legacy lines upgraded to a
// qualifying release, the e2b line downgraded to legacy) — none of it may
// show. This is the property that regressed twice during review (the
// single-resolve that hid the frozen version, then the declared-first OR), so
// it gets a direct guard rather than only inherited caller coverage.
func TestSupportsFilesystemSnapshots_ExactNeverResolves(t *testing.T) {
	t.Parallel()

	// Built but unused by the exact path — its presence documents that even
	// with an adversarial map in the environment, exact answers cannot move.
	_ = flagsWithVersionMap(t, map[string]string{
		"v1.14":   "v1.14-0.2.0",     // would upgrade legacy to qualifying
		"v1.14-0": "v1.14.1_431f1fc", // would downgrade e2b to legacy
	})

	assert.True(t, SupportsFilesystemSnapshots("v1.14-0.1.0"))
	assert.True(t, SupportsFilesystemSnapshots("v1.14-0.2.0"))
	assert.False(t, SupportsFilesystemSnapshots("v1.14.1_431f1fc"), "legacy must not qualify even when the flag would upgrade it")
	assert.False(t, SupportsFilesystemSnapshots("v1.10.1"))
	assert.False(t, SupportsFilesystemSnapshots("garbage"))
	assert.False(t, SupportsFilesystemSnapshots(""))
}

// TestSupportsFilesystemSnapshotsDeclared_ResolvesExactlyOnce pins the
// declared path's contract: one resolution through the flag, then the exact
// check on the result — in both directions, and without a second hop.
func TestSupportsFilesystemSnapshotsDeclared_ResolvesExactlyOnce(t *testing.T) {
	t.Parallel()

	flags := flagsWithVersionMap(t, map[string]string{
		// Upgrade: a non-qualifying declared version resolves to a
		// qualifying release.
		"v1.14": "v1.14-0.2.0",
		// Downgrade: a qualifying declared version resolves to legacy. This
		// entry also proves single resolution: if the result were resolved
		// AGAIN, v1.14.1_431f1fc's line ("v1.14") would hop back up to the
		// qualifying v1.14-0.2.0.
		"v1.14-0": "v1.14.1_431f1fc",
	})

	assert.True(t, SupportsFilesystemSnapshotsDeclared(t.Context(), flags, "v1.14.1_431f1fc"),
		"the flag upgrade must be applied")
	assert.False(t, SupportsFilesystemSnapshotsDeclared(t.Context(), flags, "v1.14-0.1.0"),
		"the flag downgrade must be applied — and only once, or the legacy result would hop back to qualifying")
	assert.False(t, SupportsFilesystemSnapshotsDeclared(t.Context(), flags, "garbage"),
		"unparsable declared versions fail closed")
}
