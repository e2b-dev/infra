package sandbox

import (
	"context"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func initNetworkPointers(c *Config) {
	c.egress = &atomic.Pointer[sandbox_network.Egress]{}
	c.ingress = &atomic.Pointer[sandbox_network.Ingress]{}

	e := sandbox_network.Egress{}
	c.egress.Store(&e)

	i := sandbox_network.Ingress{}
	c.ingress.Store(&i)
}

func (c *Config) GetNetworkEgress() sandbox_network.Egress {
	return *c.egress.Load()
}

func (c *Config) GetNetworkIngress() sandbox_network.Ingress {
	return *c.ingress.Load()
}

func (c *Config) SetNetworkEgress(e sandbox_network.Egress) sandbox_network.Egress {
	return *c.egress.Swap(&e)
}

func (c *Config) SetNetworkIngress(i sandbox_network.Ingress) sandbox_network.Ingress {
	return *c.ingress.Swap(&i)
}

// EgressFromProto converts a proto egress config to a sandboxnetwork.Egress.
func EgressFromProto(egress *orchestrator.SandboxNetworkEgressConfig) sandbox_network.Egress {
	if egress == nil {
		return sandbox_network.Egress{}
	}

	return sandbox_network.Egress{
		Allowed:                sandbox_network.ParseValidRules(egress.GetAllowedCidrs()),
		Denied:                 sandbox_network.ParseValidRules(egress.GetDeniedCidrs()),
		AllowedHTTPHostDomains: egress.GetAllowedDomains(),
	}
}

// IngressFromProto converts a proto ingress config to a sandboxnetwork.Ingress.
func IngressFromProto(ingress *orchestrator.SandboxNetworkIngressConfig) sandbox_network.Ingress {
	if ingress == nil {
		return sandbox_network.Ingress{}
	}

	return sandbox_network.Ingress{
		Allowed:            sandbox_network.ParseValidRules(ingress.GetAllowedCidrs()),
		Denied:             sandbox_network.ParseValidRules(ingress.GetDeniedCidrs()),
		TrafficAccessToken: ingress.GetTrafficAccessToken(),
		MaskRequestHost:    ingress.GetMaskRequestHost(),
	}
}

func getNetworkSlot(
	ctx context.Context,
	networkPool *network.Pool,
	cleanup *Cleanup,
	egress sandbox_network.Egress,
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
