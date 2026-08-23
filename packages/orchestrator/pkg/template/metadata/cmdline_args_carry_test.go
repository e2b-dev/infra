package metadata

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guest kernel cmdline variant is inherited lineage state: chosen once at build, with
// no source to re-derive it from afterwards. Every copy-constructor must therefore carry
// it, and the file round trip must preserve it, or a filesystem-only snapshot cold-boots
// on the wrong command line and the guest silently loses whatever the variant provided.
//
// Note the contrast with prefetch_carry_test.go, which pins the opposite behaviour for
// Prefetch: SameVersionTemplate deliberately DROPS that. Getting the two confused is the
// mistake these tests exist to catch.

func TestSameVersionTemplateCarriesCmdlineArgs(t *testing.T) {
	t.Parallel()

	// SameVersionTemplate is the pause path, so this is the case that matters most: it
	// runs on every pause of every opted-in sandbox, far more often than any cold boot.
	base := Template{
		Version:     CurrentVersion,
		Template:    TemplateMetadata{BuildID: "build-1"},
		Context:     Context{User: "root"},
		CmdlineArgs: map[string]string{"psi": "1"},
	}

	same := base.SameVersionTemplate(TemplateMetadata{BuildID: "build-2"})

	assert.Equal(t, map[string]string{"psi": "1"}, same.CmdlineArgs,
		"a pause must not drop the parameters a cold boot replays")
	assert.Equal(t, "build-2", same.Template.BuildID)
}

func TestCopyConstructorsCarryCmdlineArgs(t *testing.T) {
	t.Parallel()

	base := Template{
		Version:     CurrentVersion,
		Template:    TemplateMetadata{BuildID: "build-1"},
		Context:     Context{User: "root"},
		CmdlineArgs: map[string]string{"psi": "1"},
	}

	tests := map[string]Template{
		"NewVersionTemplate":  base.NewVersionTemplate(TemplateMetadata{BuildID: "build-2"}),
		"SameVersionTemplate": base.SameVersionTemplate(TemplateMetadata{BuildID: "build-2"}),
		"WithPrefetch":        base.WithPrefetch(&Prefetch{Memory: &MemoryPrefetchMapping{}}),
	}

	for name, got := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, map[string]string{"psi": "1"}, got.CmdlineArgs,
				"%s must carry the cmdline parameters", name)
		})
	}
}

// BasedOn is the one copy-constructor that must NOT carry it. It derives a build that
// starts FROM another template — a new build, resolving the flag for its own team — rather
// than continuing a lineage. Inheriting the parent's arguments would record arguments the
// child's kernel never booted with, and a later filesystem-only cold boot would apply them.
func TestBasedOnDropsCmdlineArgs(t *testing.T) {
	t.Parallel()

	base := Template{
		Version:     CurrentVersion,
		Template:    TemplateMetadata{BuildID: "build-1"},
		Context:     Context{User: "root"},
		CmdlineArgs: map[string]string{"psi": "1"},
	}

	got := base.BasedOn(FromTemplate{Alias: "a", BuildID: "build-0"})

	assert.Nil(t, got.CmdlineArgs, "a from-template build must not inherit the parent's parameters")
	assert.Equal(t, base.Context, got.Context, "the rest of the lineage state still carries")
}

func TestCmdlineArgsSurvivesFileRoundTrip(t *testing.T) {
	t.Parallel()

	base := Template{
		Version:     CurrentVersion,
		Template:    TemplateMetadata{BuildID: "build-rt", KernelVersion: "6.1", FirecrackerVersion: "1.14"},
		Context:     Context{User: "root"},
		CmdlineArgs: map[string]string{"psi": "1"},
	}

	path := filepath.Join(t.TempDir(), "metadata.json")
	require.NoError(t, base.ToFile(path))

	got, err := FromFile(path)
	require.NoError(t, err)
	// The parameters are what make a snapshot self-describing: the flag they came from
	// is edited freely and can say something different by the time this is resumed.
	assert.Equal(t, map[string]string{"psi": "1"}, got.CmdlineArgs)
}

// A template written before this field existed has no key at all, and must decode to the
// empty default — the command line every sandbox has always booted with. This is what
// makes the change need no migration.
func TestAbsentCmdlineArgsDecodesToDefault(t *testing.T) {
	t.Parallel()

	raw := `{"version":2,"template":{"build_id":"b","kernel_version":"6.1","firecracker_version":"1.14"},"context":{"user":"root"}}`

	got, err := deserialize(bytes.NewReader([]byte(raw)))
	require.NoError(t, err)
	assert.Nil(t, got.CmdlineArgs)
}

// Characterization test, not a wish: metadata is persisted by marshalling this struct, so
// a field the running binary does not know is not preserved across a read/write cycle —
// it is dropped. That is why a team may only be opted in once every node in a cluster runs
// a binary that knows the field: until then, an older node that pauses an opted-in sandbox
// strips the variant permanently, and a later cold boot comes back on the default with
// nothing to explain it.
//
// If this ever fails because the codec started preserving unknown fields, that rollout
// constraint can be relaxed — so the failure is informative rather than an annoyance.
func TestUnknownFieldsDoNotSurviveARoundTrip(t *testing.T) {
	t.Parallel()

	raw := `{"version":2,"template":{"build_id":"b"},"context":{},"a_field_from_the_future":"kept?"}`

	decoded, err := deserialize(bytes.NewReader([]byte(raw)))
	require.NoError(t, err)

	reader, err := serialize(decoded)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = buf.ReadFrom(reader)
	require.NoError(t, err)

	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.NotContains(t, out, "a_field_from_the_future",
		"unknown fields are dropped on rewrite; the rollout ordering depends on this")
}
