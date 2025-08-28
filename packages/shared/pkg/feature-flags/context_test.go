package feature_flags

import (
	"strings"
	"testing"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
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

func ldValueToText(value ldvalue.Value) string {
	return strings.ReplaceAll(value.String(), "\"", "")
}

func TestMergeContextsSameKind(t *testing.T) {
	kind := ldcontext.Kind("test")
	testKey := "test"
	emptyName := "[none]"
	emptyValue := "null"

	type keyValue struct {
		key   string
		value string
	}

	tests := []struct {
		name              string
		firstContext      ldcontext.Context
		secondContext     ldcontext.Context
		expectedName      string
		expectedKey       string
		expectedKeyValues []keyValue
	}{
		{
			name: "no_name_no_value",
			firstContext: ldcontext.NewBuilder("same").
				Kind(kind).
				Build(),
			secondContext: ldcontext.NewBuilder("same").
				Kind(kind).
				Build(),
			expectedKey:       "same",
			expectedName:      emptyName,
			expectedKeyValues: []keyValue{{key: testKey, value: emptyValue}},
		},
		{
			name: "name_no_value",
			firstContext: ldcontext.NewBuilder("same").
				Kind(kind).
				Name("first").
				Build(),
			secondContext: ldcontext.NewBuilder("same").
				Kind(kind).
				Name("second").
				Build(),
			expectedKey:       "same",
			expectedName:      "second",
			expectedKeyValues: []keyValue{{key: testKey, value: emptyValue}},
		},
		{
			name: "no_name_only_value",
			firstContext: ldcontext.NewBuilder("same").
				Kind(kind).
				SetValue(testKey, ldvalue.String("first")).
				Build(),
			secondContext: ldcontext.NewBuilder("same").
				Kind(kind).
				Build(),
			expectedKey:       "same",
			expectedName:      emptyName,
			expectedKeyValues: []keyValue{{key: testKey, value: "first"}},
		},
		{
			name: "key",
			firstContext: ldcontext.NewBuilder("first").
				Kind(kind).
				Build(),
			secondContext: ldcontext.NewBuilder("second").
				Kind(kind).
				Build(),
			expectedKey:       "second",
			expectedName:      emptyName,
			expectedKeyValues: nil,
		},
		{
			name: "combine_values",
			firstContext: ldcontext.NewBuilder("same").
				Kind(kind).
				SetValue(testKey, ldvalue.String("first")).
				Name("first").
				SetValue("other", ldvalue.String("other")).
				Build(),
			secondContext: ldcontext.NewBuilder("same").
				Kind(kind).
				SetValue(testKey, ldvalue.String("second")).
				Build(),
			expectedKey:       "same",
			expectedName:      "first",
			expectedKeyValues: []keyValue{{key: testKey, value: "second"}, {key: "other", value: "other"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mergeSameKind(tc.firstContext, tc.secondContext)
			assert.Equal(t, tc.expectedKey, result.Key(), "expected key to match")
			assert.Equal(t, tc.expectedName, result.Name().String(), "expected name to match")
			for _, kv := range tc.expectedKeyValues {
				assert.Equal(t, kv.value, ldValueToText(result.GetValue(kv.key)), "expected value for key %s to match", kv.key)
			}
		})
	}
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
