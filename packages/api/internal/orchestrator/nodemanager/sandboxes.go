package nodemanager

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager")

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

				var dbRules map[string][]types.SandboxNetworkRule
				if protoRules := egress.GetRules(); len(protoRules) > 0 {
					dbRules = make(map[string][]types.SandboxNetworkRule, len(protoRules))
					for domain, domainRules := range protoRules {
						ruleList := make([]types.SandboxNetworkRule, 0, len(domainRules.GetRules()))
						for _, r := range domainRules.GetRules() {
							dbRule := types.SandboxNetworkRule{}
							if t := r.GetTransform(); t != nil {
								dbRule.Transform = &types.SandboxNetworkTransform{
									Headers: t.GetHeaders(),
								}
							}
							ruleList = append(ruleList, dbRule)
						}
						dbRules[domain] = ruleList
					}
				}

				network.Egress = &types.SandboxNetworkEgressConfig{
					AllowedAddresses: allowedAddresses,
					DeniedAddresses:  egress.GetDeniedCidrs(),
					Rules:            dbRules,
				}
			}
		}

		volumeMounts := ConvertOrchestratorMountsToDatabaseMounts(config.GetVolumeMounts())

		var autoResume *types.SandboxAutoResumeConfig
		if autoResumeCfg := config.GetAutoResume(); autoResumeCfg != nil {
			autoResume = &types.SandboxAutoResumeConfig{
				Policy:  types.SandboxAutoResumePolicy(autoResumeCfg.GetPolicy()),
				Timeout: autoResumeCfg.GetTimeoutSeconds(),
			}
		}

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
				volumeMounts,
			),
		)
	}

	return sandboxesInfo, nil
}

func ConvertOrchestratorMountsToDatabaseMounts(mounts []*orchestrator.SandboxVolumeMount) []*types.SandboxVolumeMountConfig {
	var results []*types.SandboxVolumeMountConfig

	for _, item := range mounts {
		results = append(results, &types.SandboxVolumeMountConfig{
			ID:   item.GetId(),
			Type: item.GetType(),
			Name: item.GetName(),
			Path: item.GetPath(),
		})
	}

	return results
}
