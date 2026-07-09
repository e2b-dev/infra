package nodemanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNode_setLabels(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		input    []string
		expected map[string]struct{}
	}{
		"adds default when nil": {
			input:    nil,
			expected: map[string]struct{}{"default": {}},
		},
		"adds default when empty slice": {
			input:    []string{},
			expected: map[string]struct{}{"default": {}},
		},
		"keeps provided labels": {
			input:    []string{"foo", "bar"},
			expected: map[string]struct{}{"foo": {}, "bar": {}},
		},
		"keeps explicit default label": {
			input:    []string{"default"},
			expected: map[string]struct{}{"default": {}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := &Node{}
			node.setLabels(tc.input)

			assert.Equal(t, tc.expected, node.Labels())
		})
	}
}
