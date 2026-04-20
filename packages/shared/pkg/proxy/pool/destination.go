package pool

import (
	"net/url"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const MaskRequestHostPortPlaceholder = "${PORT}"

type DestinationContextKey struct{}

// Destination contains information about where to route the request.
type Destination struct {
	Url       *url.URL
	SandboxId string
	// LimiterKey is the key under which per-sandbox ingress connections are
	// accounted in the connection limiter. Optional: embedders that do not
	// use the limiter may leave this empty. Embedders should choose a key
	// that is unique per sandbox lifecycle — neither SandboxId (reused on
	// checkpoint/resume) nor sandbox IP (reused via the network slot pool)
	// is safe on its own.
	LimiterKey  string
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
}
