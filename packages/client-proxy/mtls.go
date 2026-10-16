package main

import (
	"google.golang.org/grpc/credentials"

	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/mtls"
)

// apiHopModeFlag is the hop's mode flag; E2B_TLS_API_CLIENT_MODE is its fallback.
const apiHopModeFlag = "mtls-client-proxy-api-client-mode"

// newAPIHop is the credentials of the in-cluster hop to the api's internal
// gRPC port.
func newAPIHop(proc *mtls.Process, c cfg.TLSConfig) (credentials.TransportCredentials, error) {
	hop, err := proc.Hop("api", apiHopModeFlag, c.API)
	if err != nil {
		return nil, err
	}

	return mtls.NewClientCredentials(hop), nil
}
