package metadata

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// TestEnvdDefaultUser pins which recorded users can be established and which cannot.
//
// The cannot cases are the point, and there are more of them than the metadata suggests.
// Determinacy is a property of the build's TEMPLATE VERSION, while the metadata records a
// USER: finalize sends Context.User at or above TemplateV2ReleaseVersion and a flat
// ("user", nil) below it, discarding what was recorded. A user-authored USER step, unlike the
// USER phase, is not version-gated — so any name can appear in the metadata of a build whose
// /init received "user". Only consts.TemplateDefaultUser is safe, because both branches send it.
//
// Returning a value for the others would overwrite a live default user on every start, not
// only on a resume, and `defaults.mismatch` could not see it: envd would agree with exactly
// what was sent.
//
// The expected users are literals, not the package constant: asserting through the constant
// would make the expectation move with the code, so a wrong value could never fail this. The
// constant's own value is pinned once, below, since finalize's compatibility branch sends it
// and this derivation is only sound while the two are the same string.
func TestEnvdDefaultUser(t *testing.T) {
	t.Parallel()

	// Pinned separately from the table so a change to the constant fails here rather than
	// silently moving every expectation with it.
	require.Equal(t, "user", consts.TemplateDefaultUser)

	for _, tc := range []struct {
		name     string
		ctx      Context
		wantUser string
		wantOK   bool
	}{
		{"the one value both finalize branches send", Context{User: "user"}, "user", true},

		// A named user reaches the metadata from an ungated USER step at any build version,
		// so it cannot be told from a build whose /init received "user".
		{"a named user is indeterminate", Context{User: "app"}, "", false},
		{"a named user with a workdir is still indeterminate", Context{User: "app", WorkDir: new("/app")}, "", false},
		{"root is indeterminate", Context{User: "root"}, "", false},
		{"no recorded user at all", Context{}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			u, ok := Template{Version: CurrentVersion, Context: tc.ctx}.EnvdDefaultUser()
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantUser, u)
		})
	}
}

// TestEnvdDefaultUserSurvivesCopyConstructors is the property the resume backfill rests on: a
// snapshot establishes what the template it descends from established. SameVersionTemplate
// runs on every pause, so a field it dropped would break the fix on the second cycle.
func TestEnvdDefaultUserSurvivesCopyConstructors(t *testing.T) {
	t.Parallel()

	tpl := Template{Version: CurrentVersion, Context: Context{User: "user", WorkDir: new("/opt/wd")}}
	wantUser, wantOK := tpl.EnvdDefaultUser()
	assert.True(t, wantOK, "fixture must be the determinate case for this to prove anything")

	for name, derived := range map[string]Template{
		"SameVersionTemplate": tpl.SameVersionTemplate(TemplateMetadata{BuildID: "b"}),
		"NewVersionTemplate":  tpl.NewVersionTemplate(TemplateMetadata{BuildID: "b"}),
		"BasedOn":             tpl.BasedOn(FromTemplate{Alias: "a", BuildID: "b"}),
		"WithPrefetch":        tpl.WithPrefetch(nil),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			gotUser, gotOK := derived.EnvdDefaultUser()
			assert.Equal(t, wantOK, gotOK)
			assert.Equal(t, wantUser, gotUser)
		})
	}
}

// TestEnvdDefaultUserOnStrippedV1Metadata exercises the V1 path through the real decoder
// rather than by hand-constructing an empty Context. deserialize() discards every field of a
// snapshot at or below DeprecatedVersion, so the claim under test is that such a snapshot can
// establish nothing — and asserting it against a zero-valued struct would pass for the wrong
// reason, and keep passing if that stripping ever stopped.
func TestEnvdDefaultUserOnStrippedV1Metadata(t *testing.T) {
	t.Parallel()

	// The recorded user must be the one value that WOULD be establishable, or the assertion
	// below passes whether or not the stripping happens and proves nothing.
	v1 := `{"version":1,"context":{"user":"user","workdir":"/app"}}`
	tpl, err := deserialize(strings.NewReader(v1))
	require.NoError(t, err)
	require.Equal(t, uint64(DeprecatedVersion), tpl.Version, "the fixture must actually be stripped")

	u, ok := tpl.EnvdDefaultUser()
	assert.False(t, ok, "a stripped snapshot records no user, so nothing can be established")
	assert.Empty(t, u)
}
