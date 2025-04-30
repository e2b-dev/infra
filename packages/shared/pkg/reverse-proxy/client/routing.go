package client

import (
	"net/url"

	"go.uber.org/zap"
)

type RoutingTargetContextKey struct{}

// RoutingTarget contains information about where to route the request.
type RoutingTarget struct {
	Url       *url.URL
	SandboxId string
	Logger    *zap.Logger
	// ConnectionKey is used for identifying which keepalive connections are not the same so we can prevent unintended reuse.
	// This is evaluated before checking for existing connection to the IP:port pair.
	ConnectionKey string
}
