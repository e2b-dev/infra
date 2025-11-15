package keys

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSHA256Hashing(t *testing.T) {
	hasher := NewSHA256Hashing()

	hashed := hasher.Hash([]byte("test"))
	assert.Regexp(t, "^\\$sha256\\$.*", hashed)
}
