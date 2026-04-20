package pool

import (
	"net/url"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const MaskRequestHostPortPlaceholder = "${PORT}"

type DestinationContextKey struct{}

// Destination contains information about where to route the request.
type Destination struct {
	Url         *url.URL
	SandboxId   string
	SandboxPort uint64
	// Should we return the error about closed port if there is a problem with a connection to upstream?
	DefaultToPortError bool
	RequestLogger      logger.Logger
	// ConnectionKey uniquely identifies a single sandbox lifecycle. It is
	// used for two purposes:
	//   1. keepalive connection pool isolation, so connections to a reused
	//      IP:port pair are not accidentally shared across sandboxes;
	//   2. per-sandbox ingress connection limiter accounting.
	// Embedders should pick a value that is unique per lifecycle. Neither
	// SandboxId (reused on checkpoint/resume) nor sandbox IP (reused via
	// the network slot pool) is safe on its own.
	ConnectionKey                      string
	IncludeSandboxIdInProxyErrorLogger bool
	// MaskRequestHost is used to mask the request host.
	MaskRequestHost *string
}
