package handler

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TT-4 (FEAT-145 W3): the process wrapper must degrade cleanly when the
// priority helpers are absent — the user command still runs; a present helper
// is applied with its resolved path.
func TestWrapperPrefix(t *testing.T) {
	t.Parallel()

	notFound := func(string) (string, error) { return "", errors.New("not found") }
	all := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	only := func(want string) func(string) (string, error) {
		return func(name string) (string, error) {
			if name == want {
				return "/bin/" + name, nil
			}

			return "", errors.New("not found")
		}
	}

	t.Run("both present", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/usr/bin/ionice -c 2 -n 4 /usr/bin/nice -n 5 ", wrapperPrefix(5, all))
	})

	t.Run("both absent degrades to bare exec", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, wrapperPrefix(5, notFound))
	})

	t.Run("only nice", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/bin/nice -n -3 ", wrapperPrefix(-3, only("nice")))
	})

	t.Run("only ionice", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/bin/ionice -c 2 -n 4 ", wrapperPrefix(0, only("ionice")))
	})
}
