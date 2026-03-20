package sandbox

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandboxnetwork "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// NewConfigWithNetwork creates a Config with network egress/ingress parsed from proto configs.
func NewConfigWithNetwork(c Config, egress *orchestrator.SandboxNetworkEgressConfig, ingress *orchestrator.SandboxNetworkIngressConfig) *Config {
	c.egress = &atomic.Pointer[sandboxnetwork.Egress]{}
	c.ingress = &atomic.Pointer[sandboxnetwork.Ingress]{}

	e := EgressFromProto(egress)
	c.egress.Store(&e)

	i := IngressFromProto(ingress)
	c.ingress.Store(&i)

	return &c
}

func (c *Config) GetNetworkEgress() sandboxnetwork.Egress {
	return *c.egress.Load()
}

func (c *Config) GetNetworkIngress() sandboxnetwork.Ingress {
	return *c.ingress.Load()
}

func (c *Config) SetNetworkEgress(e sandboxnetwork.Egress) sandboxnetwork.Egress {
	return *c.egress.Swap(&e)
}

func (c *Config) SetNetworkIngress(i sandboxnetwork.Ingress) sandboxnetwork.Ingress {
	return *c.ingress.Swap(&i)
}

// EgressFromProto converts a proto egress config to a sandboxnetwork.Egress.
func EgressFromProto(egress *orchestrator.SandboxNetworkEgressConfig) sandboxnetwork.Egress {
	if egress == nil {
		return sandboxnetwork.Egress{}
	}

	return sandboxnetwork.Egress{
		Allowed:                parseEgressRules(egress.GetAllowedCidrs()),
		Denied:                 parseEgressRules(egress.GetDeniedCidrs()),
		AllowedHTTPHostDomains: egress.GetAllowedDomains(),
	}
}

// IngressFromProto converts a proto ingress config to a sandboxnetwork.Ingress.
func IngressFromProto(ingress *orchestrator.SandboxNetworkIngressConfig) sandboxnetwork.Ingress {
	if ingress == nil {
		return sandboxnetwork.Ingress{}
	}

	return sandboxnetwork.Ingress{
		Allowed:            parseIngressRules(ingress.GetAllowed()),
		Denied:             parseIngressRules(ingress.GetDenied()),
		TrafficAccessToken: ingress.GetTrafficAccessToken(),
		MaskRequestHost:    ingress.GetMaskRequestHost(),
	}
}

// parseEgressRules converts CIDR strings into Rules for ACL matching.
func parseEgressRules(cidrs []string) sandboxnetwork.Rules {
	out := make(sandboxnetwork.Rules, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(sandboxnetwork.AddressStringToCIDR(cidr))
		if err != nil {
			continue // pre-validated by API
		}

		out = append(out, sandboxnetwork.Rule{IPNet: ipNet})
	}

	return out
}

// TODO consolidate with egress by passing host:portrange in proto
func parseIngressRules(rules []*orchestrator.IngressRule) []sandboxnetwork.Rule {
	out := make([]sandboxnetwork.Rule, 0, len(rules))
	for _, r := range rules {
		_, ipNet, err := net.ParseCIDR(r.GetCidr())
		if err != nil {
			continue // pre-validated by API
		}

		out = append(out, sandboxnetwork.Rule{
			IPNet:     ipNet,
			PortStart: uint16(r.GetPortLow()),
			PortEnd:   uint16(r.GetPortHigh()),
		})
	}

	return out
}

func getNetworkSlot(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	egress sandboxnetwork.Egress,
) *utils.Promise[*network.Slot] {
	return utils.NewPromise(func() (*network.Slot, error) {
		ctx, span := tracer.Start(ctx, "get network-slot")
		defer span.End()

		slot, err := networkPool.Get(ctx, egress)
		if err != nil {
			return nil, fmt.Errorf("failed to get network slot: %w", err)
		}

		cleanup.Add(ctx, func(ctx context.Context) error {
			ctx, span := tracer.Start(ctx, "clean network-slot")
			defer span.End()

			// We can run this cleanup asynchronously, as it is not important for the sandbox lifecycle
			go func(ctx context.Context) {
				returnErr := networkPool.Return(ctx, slot)
				if returnErr != nil {
					logger.L().Error(ctx, "failed to return network slot", zap.Error(returnErr))
				}
			}(context.WithoutCancel(ctx))

			return nil
		})

		return slot, nil
	})
}
