package cfg

import "github.com/e2b-dev/infra/packages/shared/pkg/mtls"

// TLSConfig is the mutual TLS settings the chart renders for
// dashboard-api's listener.
type TLSConfig struct {
	HTTP mtls.ListenerSettings `envPrefix:"E2B_TLS_HTTP_"`
}
