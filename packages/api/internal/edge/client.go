package edge

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

type clientAuthorization struct {
	secret string
	tls    bool
}

func (a clientAuthorization) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{consts.EdgeRpcAuthHeader: a.secret}, nil
}

func (a clientAuthorization) RequireTransportSecurity() bool {
	return a.tls
}
