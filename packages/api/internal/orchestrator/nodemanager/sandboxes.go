package nodemanager

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func (n *Node) GetSandboxes(ctx context.Context) ([]sandbox.Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-sandboxes-from-orchestrator")
	defer childSpan.End()

	client, childCtx := n.GetClient(childCtx)
	res, err := client.Sandbox.List(childCtx, &empty.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	sandboxes := res.GetSandboxes()

	sandboxesInfo := make([]sandbox.Sandbox, 0, len(sandboxes))

	for _, sbx := range sandboxes {
		config := sbx.GetConfig()

		if config == nil {
			return nil, fmt.Errorf("sandbox config is nil when listing sandboxes: %#v", sbx)
		}

		teamID, parseErr := uuid.Parse(config.GetTeamId())
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse team ID '%s' for job: %w", config.GetTeamId(), parseErr)
		}

		buildID, parseErr := uuid.Parse(config.GetBuildId())
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse build ID '%s' for job: %w", config.GetBuildId(), parseErr)
		}

		var networkTrafficAccessToken *string
		if ingress := config.GetNetwork().GetIngress(); ingress != nil {
			networkTrafficAccessToken = ingress.TrafficAccessToken
		}

		var network *types.SandboxNetworkConfig
		if config.GetNetwork() != nil {
			network = &types.SandboxNetworkConfig{}

			if ingress := config.GetNetwork().GetIngress(); ingress != nil {
				network.Ingress = &types.SandboxNetworkIngressConfig{
					AllowPublicAccess: ut.ToPtr(networkTrafficAccessToken == nil),
					MaskRequestHost:   ingress.MaskRequestHost,
				}
			}

			if egress := config.GetNetwork().GetEgress(); egress != nil {
				// Combine allowed CIDRs and domains back into AllowedAddresses
				allowedAddresses := slices.Concat(egress.GetAllowedCidrs(), egress.GetAllowedDomains())
				network.Egress = &types.SandboxNetworkEgressConfig{
					AllowedAddresses: allowedAddresses,
					DeniedAddresses:  egress.GetDeniedCidrs(),
				}
			}
		}

		autoResume := ut.CastPtr(config.GetAutoResume(), func(v string) types.SandboxAutoResumePolicy {
			return types.SandboxAutoResumePolicy(v)
		})

		sandboxesInfo = append(
			sandboxesInfo,
			sandbox.NewSandbox(
				config.GetSandboxId(),
				config.GetTemplateId(),
				consts.ClientID,
				config.Alias, //nolint:protogetter // we need the nil check too
				config.GetExecutionId(),
				teamID,
				buildID,
				config.GetMetadata(),
				time.Duration(config.GetMaxSandboxLength())*time.Hour,
				sbx.GetStartTime().AsTime(),
				sbx.GetEndTime().AsTime(),
				config.GetVcpu(),
				config.GetTotalDiskSizeMb(),
				config.GetRamMb(),
				config.GetKernelVersion(),
				config.GetFirecrackerVersion(),
				config.GetEnvdVersion(),
				n.ID,
				n.ClusterID,
				config.GetAutoPause(),
				autoResume,
				config.EnvdAccessToken,     //nolint:protogetter // we need the nil check too
				config.AllowInternetAccess, //nolint:protogetter // we need the nil check too
				config.GetBaseTemplateId(),
				n.SandboxDomain,
				network,
				networkTrafficAccessToken,
			),
		)
	}

	return sandboxesInfo, nil
}
