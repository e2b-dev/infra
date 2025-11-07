package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInvalidSandboxPortError_Is(t *testing.T) {
	var e InvalidSandboxPortError

	t.Run("error is nil", func(t *testing.T) {
		ok := e.Is(nil)
		assert.False(t, ok)
	})

	t.Run("error is port error", func(t *testing.T) {
		var e2 InvalidSandboxPortError
		ok := e.Is(e2)
		assert.True(t, ok)
	})

	t.Run("error is not port error", func(t *testing.T) {
		var e2 SandboxNotFoundError
		ok := e.Is(e2)
		assert.False(t, ok)
	})
}
