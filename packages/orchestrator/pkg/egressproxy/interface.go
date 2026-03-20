package egressproxy

import (
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

var _ sandbox.MapSubscriber = (EgressProxy)(nil)

type EgressProxy interface {
	sandbox.MapSubscriber

	// SlotEventLifecycle used for registering network slot creation/removal lifecycle.
	network.SlotEventLifecycle
}
