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
	"golang.org/x/sync/errgroup"
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

var ErrUnknownVolumeType = errors.New("unknown volume type")

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

	var (
		receivedUnknownVolumeTypeErrors int
		unknownVolumeType               string
		notReadyNodeCount               int
	)
	defer func() {
		if receivedUnknownVolumeTypeErrors != 0 {
			logger.L().Warn(ctx, "received unknown volume type errors",
				zap.String("volume_type", unknownVolumeType),
				zap.Int("total_nodes", len(nodes)),
				zap.Int("unknown_type_errors", receivedUnknownVolumeTypeErrors),
			)
		}
	}()

	for _, node := range nodes {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context error: %w", err)
		}

		if node.Status() != api.NodeStatusReady {
			notReadyNodeCount++

			continue
		}

		c, clientCtx := node.GetClient(ctx)

		err := fn(clientCtx, c)
		if err == nil { // inverted guard clause, the rest of the function is chunky
			return nil
		}

		// the rest is all error handling

		if ctxErr := ctx.Err(); ctxErr != nil {
			// original context is canceled, bail
			return errors.Join(
				fmt.Errorf("orchestrator error: %w", err),
				fmt.Errorf("request error: %w", ctxErr),
			)
		}

		// we want to retry these, but we don't want to flood the logs with reports
		if volumeType, ok := isUnknownVolumeTypeError(err); ok {
			unknownVolumeType = volumeType
			receivedUnknownVolumeTypeErrors++

			continue
		}

		if isRetryableError(err) {
			logger.L().Warn(clientCtx, "failed to make orchestrator call, retrying ... ", zap.Error(err))

			continue
		}

		return err
	}

	if receivedUnknownVolumeTypeErrors == len(nodes)-notReadyNodeCount && receivedUnknownVolumeTypeErrors > 0 {
		return fmt.Errorf("%w: %s", ErrUnknownVolumeType, unknownVolumeType)
	}

	return ErrNoHealthyOrchestratorFound
}

func isUnknownVolumeTypeError(err error) (string, bool) {
	grpcStatus, ok := status.FromError(err)
	if ok {
		for _, actual := range grpcStatus.Details() {
			if vterr, ok := actual.(*orchestrator.UnknownVolumeTypeError); ok {
				return vterr.GetVolumeType(), true // maybe there's another orchestrator that knows about it?
			}
		}
	}

	return "", false
}

func isRetryableError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	return false
}

// executeOnAllClusterNodes calls fn concurrently on every ready node in the
// cluster and returns a combined error if any node fails. This is used for
// volume operations so that the volume directory exists on every orchestrator
// node, regardless of which node a sandbox is later scheduled on.
//
// All goroutines always run to completion regardless of individual failures so
// that partial state (e.g. a volume directory left on some nodes but not
// others) is avoided.
func (a *APIStore) executeOnAllClusterNodes(
	ctx context.Context,
	clusterID uuid.UUID,
	fn func(context.Context, *clusters.GRPCClient) error,
) error {
	nodes := a.orchestrator.GetClusterNodes(clusterID)

	if len(nodes) == 0 {
		return ErrClusterNotFound
	}

	// Use a plain errgroup without context so that a failure on one node does
	// not cancel in-flight or pending RPCs on other nodes.
	var wg errgroup.Group

	readyNodeCount := 0

	for _, node := range nodes {
		if node.Status() != api.NodeStatusReady {
			continue
		}

		readyNodeCount++

		wg.Go(func() error {
			// Derive a per-call context from the original request context so
			// that the caller's deadline/cancellation is still respected, but
			// a failure on one node does not affect the others.
			c, clientCtx := node.GetClient(ctx)

			if err := fn(clientCtx, c); err != nil {
				if volumeType, ok := isUnknownVolumeTypeError(err); ok {
					return fmt.Errorf("%w: %s", ErrUnknownVolumeType, volumeType)
				}

				return fmt.Errorf("node %s: %w", node.ID, err)
			}

			return nil
		})
	}

	if readyNodeCount == 0 {
		return ErrNoHealthyOrchestratorFound
	}

	return wg.Wait()
}
