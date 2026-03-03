package handlers

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func toVolumeKey(volume queries.Volume) *orchestrator.VolumeInfo {
	return &orchestrator.VolumeInfo{
		VolumeId:   volume.ID.String(),
		VolumeType: volume.VolumeType,
		TeamId:     volume.TeamID.String(),
	}
}

func (a *APIStore) getVolume(c *gin.Context, volumeID string) (queries.Volume, *types.Team, bool) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return queries.Volume{}, team, false
	}

	telemetry.SetAttributes(ctx,
		telemetry.WithTeamID(team.ID.String()),
	)

	volumeIDuuid, err := uuid.Parse(volumeID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid volume ID")
		telemetry.ReportCriticalError(ctx, "error when parsing volume ID", err)

		return queries.Volume{}, team, false
	}

	volume, err := a.sqlcDB.GetVolume(ctx, queries.GetVolumeParams{
		VolumeID: volumeIDuuid,
		TeamID:   team.ID,
	})

	switch {
	case dberrors.IsNotFoundError(err):
		a.sendAPIStoreError(c, http.StatusNotFound, "Volume not found")
		telemetry.ReportError(ctx, "volume not found", err)

		return volume, team, false
	case err != nil:
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting volume")
		telemetry.ReportCriticalError(ctx, "error when getting volume", err)

		return volume, team, false
	default:
		return volume, team, true
	}
}

var ErrClusterNotFound = errors.New("cluster not found")

var ErrNoHealthyOrchestratorFound = errors.New("no healthy orchestrator found")

func (a *APIStore) executeOnOrchestratorByClusterID(
	ctx context.Context,
	clusterID uuid.UUID,
	fn func(context.Context, *clusters.GRPCClient) error,
) error {
	nodes := a.orchestrator.GetClusterNodes(clusterID)

	if len(nodes) == 0 {
		return ErrClusterNotFound
	}

	rand.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })

	for _, node := range nodes {
		if node.Status() != api.NodeStatusReady {
			continue
		}

		c, clientCtx := node.GetClient(ctx)

		if clientErr := fn(clientCtx, c); clientErr != nil {
			if err := ctx.Err(); err != nil {
				// original context is canceled, bail
				return errors.Join(
					fmt.Errorf("orchestrator error: %w", clientErr),
					fmt.Errorf("request error: %w", err),
				)
			}

			if isRetryableError(clientErr) {
				logger.L().Warn(clientCtx, "failed to make orchestrator call, retrying ... ", zap.Error(clientErr))

				continue
			}

			return clientErr
		}

		return nil
	}

	return ErrNoHealthyOrchestratorFound
}

func isRetryableError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	grpcStatus, ok := status.FromError(err)
	if ok {
		for _, actual := range grpcStatus.Details() {
			if _, ok := actual.(*orchestrator.UnknownVolumeTypeError); ok {
				return true // maybe there's another orchestrator that knows about it?
			}
		}
	}

	return false
}
