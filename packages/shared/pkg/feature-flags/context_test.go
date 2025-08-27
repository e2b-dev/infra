package feature_flags

import (
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/stretchr/testify/assert"
)

func TestFlattenContexts(t *testing.T) {
	one := ldcontext.NewWithKind("one", "one")
	two := ldcontext.NewWithKind("two", "two")
	three := ldcontext.NewWithKind("three", "three")
	four := ldcontext.NewWithKind("four", "four")
	five := ldcontext.NewWithKind("five", "five")
	six := ldcontext.NewWithKind("six", "six")
	seven := ldcontext.NewWithKind("seven", "seven")

	items := []ldcontext.Context{one, two, ldcontext.NewMulti(three, four, ldcontext.NewMulti(five, six)), seven}

	result := flattenContexts(items)

	assert.Equal(t, []ldcontext.Context{one, two, three, four, five, six}, result)
}
