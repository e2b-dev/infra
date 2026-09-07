//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// binaryCacheFF returns a flag client with envd-binary-cache forced to on/off.
func binaryCacheFF(t *testing.T, on bool) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.EnvdBinaryCacheFlag.Key()).VariationForAll(on))

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.WithoutCancel(t.Context())) })

	return ff
}

// The startup warm is the whole reason a resume never waits for the copy, and it
// is wiring: no unit of the cache itself can tell whether NewFactory actually
// calls it. It went missing once, silently, and every other test still passed.
func TestNewFactoryWarmsThePromotedBinary(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	// This one needs a stand-in that BOTH parses as an ELF image and actually
	// runs: NewFactory builds the cache with the real version probe, which execs
	// the copy, and the cache validates the copy's structure before doing so. A
	// shell script fails the structure check and a hand-written header fails the
	// exec, so the fixture is compiled. Copying a system binary does not work
	// either -- coreutils ships as a multi-call binary that dispatches on argv[0]
	// and refuses to run under another name.
	src := filepath.Join(base, "envd")
	buildStandInEnvd(t, src, "9.9.9")

	f := NewFactory(
		t.Context(),
		cfg.BuilderConfig{OrchestratorBaseDir: base, HostEnvdPath: src},
		nil, nil, binaryCacheFF(t, true), nil, nil, nil, nil, nil,
	)

	// Asynchronous by design, so Eventually is the right tool; nothing here
	// asserts how long it took.
	require.Eventually(t, func() bool {
		_, ok := f.EnvdBinCache().Lookup(src)

		return ok
	}, 10*time.Second, 20*time.Millisecond,
		"NewFactory must warm the promoted binary, so the first resume is a hit rather than a miss")

	entry, ok := f.EnvdBinCache().Lookup(src)
	require.True(t, ok)
	require.Equal(t, "9.9.9", entry.Version, "the probe really ran the copy")
	require.NotEqual(t, src, entry.LocalPath, "the entry must point at the local copy")
}

func TestEnvdBinCacheDirIsEmptyWithoutABase(t *testing.T) {
	t.Parallel()

	// filepath.Join("", …) would yield "envdbin", a path under whatever the
	// process's working directory happens to be — and a Factory built from a
	// zero-value config (as other tests do) would then create it.
	require.Empty(t, envdBinCacheDir(""))
	require.Equal(t, filepath.Join("/orchestrator", "envdbin"), envdBinCacheDir("/orchestrator"))
}

// The other half of the flag: with the cache off, a boot must not read 13 MB or
// leave a directory behind. "Flag off = no observable change" includes this.
func TestNewFactoryDoesNotWarmWhenTheFlagIsOff(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	src := filepath.Join(base, "envd")
	writeEnvdImage(t, src, "9.9.9")

	f := NewFactory(
		t.Context(),
		cfg.BuilderConfig{OrchestratorBaseDir: base, HostEnvdPath: src},
		nil, nil, binaryCacheFF(t, false), nil, nil, nil, nil, nil,
	)

	// Nothing to wait for, so assert the absence directly after giving a warm the
	// chance to have happened at all.
	require.Never(t, func() bool {
		_, ok := f.EnvdBinCache().Lookup(src)

		return ok
	}, 2*time.Second, 50*time.Millisecond, "the cache must stay empty with the flag off")

	require.NoDirExists(t, filepath.Join(base, "envdbin"))
}

// buildStandInEnvd compiles a stand-in envd at path that answers -version with
// version. Compiled rather than faked because this is the one test whose subject
// is the real probe: it execs the copy the warm makes, and nothing short of a
// working binary exercises that.
func buildStandInEnvd(t *testing.T, path, version string) {
	t.Helper()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "go.mod"),
		[]byte("module standinenvd\n\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\""+version+"\") }\n"), 0o644))

	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", path, ".")
	cmd.Dir = srcDir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building the stand-in envd: %s", out)
}

// binaryCacheFFWithTarget is binaryCacheFF plus a live upgrade target, so a test
// can drive the pinned-version ramp rather than only the promoted one.
func binaryCacheFFWithTarget(t *testing.T, target string) *featureflags.Client {
	t.Helper()

	td := ldtestdata.DataSource()
	td.Update(td.Flag(featureflags.EnvdBinaryCacheFlag.Key()).VariationForAll(true))
	td.Update(td.Flag(featureflags.EnvdUpgradeTargetFlag.Key()).ValueForAll(ldvalue.String(target)))

	ff, err := featureflags.NewClientWithDatasource(td)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ff.Close(context.WithoutCancel(t.Context())) })

	return ff
}

// A version-pinned ramp is the normal way to canary an envd build, and it is the
// shape the startup warm used to miss entirely: the resolver asks for the staged
// sibling, so warming only the promoted path left that ramp with no pre-warm while
// still paying for one. Nothing else notices -- the resume simply defers and the
// operator reads it as a cold node.
func TestNewFactoryWarmsAPinnedTargetBesideThePromotedBinary(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	promoted := filepath.Join(base, "envd")
	staged := filepath.Join(base, "envd.9f9f9f9")
	buildStandInEnvd(t, promoted, "9.9.9")
	buildStandInEnvd(t, staged, "9.9.10")

	f := NewFactory(
		t.Context(),
		cfg.BuilderConfig{OrchestratorBaseDir: base, HostEnvdPath: promoted},
		nil, nil, binaryCacheFFWithTarget(t, "9f9f9f9"), nil, nil, nil, nil, nil,
	)

	// Both, not either: the promoted path stays warmed because a target flag can
	// turn on later with nothing to re-run this, and the staged one is what THIS
	// ramp's resumes will ask for.
	require.Eventually(t, func() bool {
		_, promotedOK := f.EnvdBinCache().Lookup(promoted)
		_, stagedOK := f.EnvdBinCache().Lookup(staged)

		return promotedOK && stagedOK
		// A liveness bound, not a latency assertion: the claim is that both targets
		// are warmed, not that warming is quick. These tests share a runner with the
		// rest of the suite, so a tight bound would measure the machine.
	}, 60*time.Second, 20*time.Millisecond,
		"the startup warm must prime the pinned target as well as the promoted binary")

	entry, ok := f.EnvdBinCache().Lookup(staged)
	require.True(t, ok)
	require.Equal(t, "9.9.10", entry.Version, "the staged copy was probed, not the promoted one")
}
