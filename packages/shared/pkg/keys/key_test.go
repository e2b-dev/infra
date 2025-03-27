package keys

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskKey(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		masked, err := MaskKey("test_", "1234567890")
		assert.NoError(t, err)
		assert.Equal(t, "test_******7890", masked)
	})

	t.Run("too short key value", func(t *testing.T) {
		_, err := MaskKey("test", "123")
		assert.Error(t, err)
	})

	t.Run("empty prefix", func(t *testing.T) {
		masked, err := MaskKey("", "1234567890")
		assert.NoError(t, err)
		assert.Equal(t, "******7890", masked)
	})
}

func TestGenerateKey(t *testing.T) {
	t.Run("succeeds", func(t *testing.T) {
		key, err := GenerateKey("test_")
		assert.NoError(t, err)
		assert.Regexp(t, "^test_.*", key.PrefixedRawValue)
		assert.Regexp(t, "^test_\\*+[0-9a-f]{4}$", key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})

	t.Run("no prefix", func(t *testing.T) {
		key, err := GenerateKey("")
		assert.NoError(t, err)
		assert.Regexp(t, "^[0-9a-f]{40}$", key.PrefixedRawValue)
		assert.Regexp(t, "^\\*+[0-9a-f]{4}$", key.MaskedValue)
		assert.Regexp(t, "^\\$sha256\\$.*", key.HashedValue)
	})
}
