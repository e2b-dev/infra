package tcpfirewall

import (
	"context"
	"fmt"
	"net"
	"strings"

	xproxy "golang.org/x/net/proxy"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

const sandboxIDPlaceholder = "{{sandboxID}}"

// socks5AuthFromEgress builds SOCKS5 credentials from egress config.
// Username and password support {{sandboxID}} placeholder substitution,
// allowing proxy operators to identify which sandbox is making requests
// (e.g. username "customer-session_{{sandboxID}}").
func socks5AuthFromEgress(egress *orchestrator.SandboxNetworkEgressConfig, sandboxID string) *xproxy.Auth {
	user := egress.GetEgressProxyUsername()
	pass := egress.GetEgressProxyPassword()
	if user == "" {
		return nil
	}

	return &xproxy.Auth{
		User:     strings.ReplaceAll(user, sandboxIDPlaceholder, sandboxID),
		Password: strings.ReplaceAll(pass, sandboxIDPlaceholder, sandboxID),
	}
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
