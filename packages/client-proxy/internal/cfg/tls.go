package cfg

import "github.com/e2b-dev/infra/packages/shared/pkg/mtls"

// TLSConfig is the mutual TLS settings the chart renders for
// client-proxy's client hop to the api.
type TLSConfig struct {
	API mtls.HopSettings `envPrefix:"E2B_TLS_API_"`
}
