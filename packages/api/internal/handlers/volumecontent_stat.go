package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var ErrExpectedEntry = errors.New("expected entry")

func (a *APIStore) GetVolumesVolumeIDStat(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDStatParams) {
	a.executeOnOrchestratorByVolumeID(c, volumeID, func(ctx context.Context, volume queries.Volume, client *clusters.GRPCClient) error {
		stat, err := client.Volumes.Stat(ctx, &orchestrator.StatRequest{
			Volume: toVolumeKey(volume),
			Path:   params.Path,
		})
		if err != nil {
			return err
		}

		entry := stat.GetEntry()
		if entry != nil {
			return ErrExpectedEntry
		}

		result := toVolumeEntryStat(entry)

		c.JSON(http.StatusOK, api.GetVolumesVolumeIDStatResponse{
			JSON200: &result,
		})

		return nil
	})
}

func toVolumeEntryStat(entry *orchestrator.EntryInfo) api.VolumeEntryStat {
	return api.VolumeEntryStat{
		Ctime:  entry.GetCreatedTime().AsTime(),
		Group:  entry.GetGroup(),
		Mode:   entry.GetMode(),
		Mtime:  entry.GetModifiedTime().AsTime(),
		Name:   entry.GetName(),
		Owner:  entry.GetOwner(),
		Path:   entry.GetPath(),
		Size:   entry.GetSize(),
		Target: entry.SymlinkTarget,
		Type:   toType(entry.GetType()),
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
