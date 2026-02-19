package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	clustershared "github.com/e2b-dev/infra/packages/shared/pkg/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) executeOnOrchestratorByVolumeID(
	c *gin.Context,
	volumeID api.VolumeID,
	fn func(context.Context, queries.Volume, *clusters.GRPCClient) error,
) {
	ctx := c.Request.Context()

	team, apiErr := a.GetTeam(ctx, c, nil)
	if apiErr != nil {
		a.sendAPIStoreError(c, apiErr.Code, apiErr.ClientMsg)
		telemetry.ReportCriticalError(ctx, "error when getting team", apiErr.Err)

		return
	}

	volumeIDuuid, err := uuid.Parse(volumeID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "invalid volume ID")
		telemetry.ReportError(ctx, "invalid volume ID", err)

		return
	}

	volume, err := a.sqlcDB.GetVolume(ctx, queries.GetVolumeParams{
		VolumeID: volumeIDuuid,
		TeamID:   team.ID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			a.sendAPIStoreError(c, http.StatusNotFound, "volume not found")

			return
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to get volume")
		telemetry.ReportCriticalError(ctx, "error when getting volume", err)

		return
	}

	clusterID := clustershared.WithClusterFallback(team.ClusterID)

	if err := a.executeOnOrchestratorByClusterID(ctx, clusterID, func(ctx context.Context, client *clusters.GRPCClient) error {
		return fn(ctx, volume, client)
	}); err != nil {
		if errors.Is(err, ErrClusterNotFound) {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "cluster not found")
			telemetry.ReportError(ctx, "cluster not found", err)

			return
		}

		if code, ok := status.FromError(err); ok {
			switch code.Code() {
			case codes.AlreadyExists:
				a.sendAPIStoreError(c, http.StatusConflict, "file already exists")

				return

			case codes.NotFound:
				a.sendAPIStoreError(c, http.StatusNotFound, "path not found")

				return

			case codes.InvalidArgument:
				a.sendAPIStoreError(c, http.StatusBadRequest, "invalid argument")

				return

			case codes.FailedPrecondition:
				a.sendAPIStoreError(c, http.StatusPreconditionFailed, "failed precondition")

				return
			}
		}

		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to execute on orchestrator")
		telemetry.ReportCriticalError(ctx, "error when executing on orchestrator", err)

		return
	}
}

func toVolumeKey(volume queries.Volume) *orchestrator.VolumeInfo {
	return &orchestrator.VolumeInfo{
		VolumeId:   volume.ID.String(),
		VolumeType: volume.VolumeType,
		TeamId:     volume.TeamID.String(),
	}
}

func toVolumeEntryStat(entry *orchestrator.EntryInfo) api.VolumeEntryStat {
	return api.VolumeEntryStat{
		Atime:  entry.GetAccessedTime().AsTime(),
		Ctime:  entry.GetCreatedTime().AsTime(),
		Gid:    entry.GetGid(),
		Mode:   entry.GetMode(),
		Mtime:  entry.GetModifiedTime().AsTime(),
		Name:   entry.GetName(),
		Path:   entry.GetPath(),
		Size:   entry.GetSize(),
		Target: entry.SymlinkTarget,
		Type:   toType(entry.GetType()),
		Uid:    entry.GetUid(),
	}
}

func toType(getType orchestrator.FileType) api.VolumeEntryStatType {
	switch getType {
	case orchestrator.FileType_FILE_TYPE_DIRECTORY:
		return api.Directory
	case orchestrator.FileType_FILE_TYPE_FILE:
		return api.File
	case orchestrator.FileType_FILE_TYPE_SYMLINK:
		return api.Symlink
	default:
		return api.Unknown
	}
}
