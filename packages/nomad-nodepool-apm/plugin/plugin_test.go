package plugin

import (
	"testing"

	"github.com/hashicorp/nomad/api"
)

func TestCountReadyNodesIncludesIneligibleNodes(t *testing.T) {
	t.Parallel()

	nodes := []*api.NodeListStub{
		{Status: api.NodeStatusReady, SchedulingEligibility: api.NodeSchedulingEligible},
		{Status: api.NodeStatusReady, SchedulingEligibility: api.NodeSchedulingIneligible},
		{Status: api.NodeStatusDown, SchedulingEligibility: api.NodeSchedulingEligible},
	}

	if got := countReadyNodes(nodes); got != 2 {
		t.Fatalf("ready node count = %d, want 2", got)
	}
}
