package tcpfirewall

import (
	"context"
	"fmt"
	"net"

	xproxy "golang.org/x/net/proxy"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

func socks5AuthFromEgress(egress *orchestrator.SandboxNetworkEgressConfig) *xproxy.Auth {
	user := egress.GetEgressProxyUsername()
	pass := egress.GetEgressProxyPassword()
	if user == "" {
		return nil
	}

	return &xproxy.Auth{User: user, Password: pass}
}

func newSOCKS5DialContext(proxyAddr string, auth *xproxy.Auth) (dialContextFunc, error) {
	dialer, err := xproxy.SOCKS5("tcp", proxyAddr, auth, xproxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("create SOCKS5 dialer for %q: %w", proxyAddr, err)
	}

	ctxDialer, ok := dialer.(xproxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS5 dialer does not support DialContext")
	}

	return ctxDialer.DialContext, nil
}
