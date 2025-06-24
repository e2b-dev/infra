package edge

import (
	"context"
)

type clientAuthorization struct {
	secret string
	tls    bool
}

func (a clientAuthorization) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{edgeRpcAuthHeader: a.secret}, nil
}

func (a clientAuthorization) RequireTransportSecurity() bool {
	return a.tls
}
