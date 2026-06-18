package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
)

func TestFindNodesByVolumeLabel(t *testing.T) {
	t.Parallel()

	newNode := func(id string, labels ...string) *nodemanager.Node {
		return nodemanager.NewTestNode(id, api.NodeStatusReady, 0, 1, nodemanager.WithLabels(labels))
	}

	matchedIDs := func(nodes []*nodemanager.Node) []string {
		var ids []string
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}

		return ids
	}

	cases := map[string]struct {
		nodes             []*nodemanager.Node
		expectedLabel     string
		expectedMatched   []string
		expectedUnmatched []string
	}{
		"splits matched and unmatched preserving order": {
			nodes: []*nodemanager.Node{
				newNode("a", "ssd"),
				newNode("b", "default"),
				newNode("c", "ssd", "default"),
				newNode("d", "hdd"),
			},
			expectedLabel:     "ssd",
			expectedMatched:   []string{"a", "c"},
			expectedUnmatched: []string{"b", "d"},
		},
		"all nodes match": {
			nodes: []*nodemanager.Node{
				newNode("a", "ssd"),
				newNode("b", "ssd"),
			},
			expectedLabel:     "ssd",
			expectedMatched:   []string{"a", "b"},
			expectedUnmatched: nil,
		},
		"no nodes match": {
			nodes: []*nodemanager.Node{
				newNode("a", "hdd"),
				newNode("b", "default"),
			},
			expectedLabel:     "ssd",
			expectedMatched:   nil,
			expectedUnmatched: []string{"a", "b"},
		},
		"empty input": {
			nodes:             nil,
			expectedLabel:     "ssd",
			expectedMatched:   nil,
			expectedUnmatched: nil,
		},
		"node with no labels never matches": {
			nodes:             []*nodemanager.Node{newNode("a")},
			expectedLabel:     "default",
			expectedMatched:   nil,
			expectedUnmatched: []string{"a"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			matched, unmatched := findNodesByVolumeLabel(tc.nodes, tc.expectedLabel)

			assert.Equal(t, tc.expectedMatched, matchedIDs(matched))
			assert.Equal(t, tc.expectedUnmatched, matchedIDs(unmatched))
		})
	}
}
