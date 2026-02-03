package pool

import (
	"net/http"
	"net/url"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const MaskRequestHostPortPlaceholder = "${PORT}"

type DestinationContextKey struct{}

// ProxyErrorHandler allows callers to override proxy error handling.
// Return true when the error has been handled and the default handler should be skipped.
type ProxyErrorHandler func(w http.ResponseWriter, r *http.Request, err error) bool

// Destination contains information about where to route the request.
type Destination struct {
	Url         *url.URL
	SandboxId   string
	SandboxPort uint64
	// Should we return the error about closed port if there is a problem with a connection to upstream?
	DefaultToPortError bool
	RequestLogger      logger.Logger
	// ConnectionKey is used for identifying which keepalive connections are not the same so we can prevent unintended reuse.
	// This is evaluated before checking for existing connection to the IP:port pair.
	ConnectionKey                      string
	IncludeSandboxIdInProxyErrorLogger bool
	// MaskRequestHost is used to mask the request host.
	MaskRequestHost *string
	// OnProxyError is called when proxying to the upstream fails.
	OnProxyError ProxyErrorHandler
}
