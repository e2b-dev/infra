//go:build linux

package base

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/storage/cache"
)

// cmdlineHashKeys mirrors the contribution Hash makes for a variant. Hash itself needs an
// index and a feature-flag client, so the property worth pinning — that the contribution is
// absent for an untargeted team, stable across map order, and distinct per argument set — is
// tested against the key-building directly.
func cmdlineHashKeys(base []string, args map[string]string) string {
	keys := slices.Clone(base)
	if len(args) > 0 {
		keys = append(keys, "cmdline:"+fc.KernelArgs(args).String())
	}

	return cache.HashKeys(keys[0], keys[1:]...)
}

func TestCmdlineArgsCacheKey(t *testing.T) {
	t.Parallel()

	base := []string{"index-v1", "provision-1", "1024", "ubuntu:22.04"}
	unTargeted := cmdlineHashKeys(base, nil)

	t.Run("an untargeted team's key is unchanged", func(t *testing.T) {
		t.Parallel()

		// The fleet-wide-rebuild guard: a team with no variant must hash exactly as it
		// did before the field existed, or every cached base layer everywhere is invalidated.
		assert.Equal(t, cache.HashKeys(base[0], base[1:]...), unTargeted)
	})

	t.Run("no parameters does not change the key", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, unTargeted, cmdlineHashKeys(base, nil))
	})

	t.Run("arguments change the key", func(t *testing.T) {
		t.Parallel()

		withPSI := cmdlineHashKeys(base, map[string]string{"psi": "1"})
		assert.NotEqual(t, unTargeted, withPSI)

		// Removing a team's variant returns them to the key their old layers are under.
		assert.Equal(t, unTargeted, cmdlineHashKeys(base, nil))
	})

	t.Run("different arguments differ", func(t *testing.T) {
		t.Parallel()

		a := cmdlineHashKeys(base, map[string]string{"psi": "1"})
		b := cmdlineHashKeys(base, map[string]string{"nokaslr": ""})
		assert.NotEqual(t, a, b)
	})

	t.Run("the same arguments hash the same regardless of map order", func(t *testing.T) {
		t.Parallel()

		// Go randomises map iteration, so an unsorted key would cache nothing at all.
		want := cmdlineHashKeys(base, map[string]string{"psi": "1", "nokaslr": "", "a": "b"})
		for range 20 {
			assert.Equal(t, want, cmdlineHashKeys(base, map[string]string{"a": "b", "nokaslr": "", "psi": "1"}))
		}
	})
}
