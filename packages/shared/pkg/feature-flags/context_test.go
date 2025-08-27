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

	// we don't care what order the contexts are in, they just all need to be there
	keySet := sliceToSet(result, func(c ldcontext.Context) ldcontext.Kind { return c.Kind() })
	assert.Equal(t, newSet[ldcontext.Kind]("one", "two", "three", "four", "five", "six", "seven"), keySet)
}

type set[T comparable] map[T]struct{}

func sliceToSet[TIn any, TOut comparable](input []TIn, fn func(TIn) TOut) set[TOut] {
	s := newSet[TOut]()

	for _, item := range input {
		key := fn(item)
		s[key] = struct{}{}
	}

	return s
}

func newSet[T comparable](input ...T) set[T] {
	s := make(set[T])

	for _, item := range input {
		s[item] = struct{}{}
	}

	return s
}
