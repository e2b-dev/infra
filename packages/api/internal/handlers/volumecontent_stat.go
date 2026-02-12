package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/clusters"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

var ErrExpectedEntry = errors.New("expected entry")

func (a *APIStore) GetVolumesVolumeIDStat(c *gin.Context, volumeID api.VolumeID, params api.GetVolumesVolumeIDStatParams) {
	a.executeOnOrchestrator(c, func(ctx context.Context, client *clusters.GRPCClient) error {
		stat, err := client.Volumes.Stat(ctx, &orchestrator.StatRequest{
			VolumeId: volumeID,
			Path:     params.Path,
		})
		if err != nil {
			return err
		}

		entry := stat.GetEntry()
		if entry != nil {
			return ErrExpectedEntry
		}

		result := toVolumeStat(entry)

		c.JSON(http.StatusOK, api.GetVolumesVolumeIDStatResponse{
			JSON200: &result,
		})

		return nil
	})
}

func toVolumeStat(entry *orchestrator.EntryInfo) api.VolumeStat {
	return api.VolumeStat{
		Ctime:       entry.GetCreatedTime().AsTime(),
		Group:       entry.GetGroup(),
		Mode:        entry.GetMode(),
		Mtime:       entry.GetModifiedTime().AsTime(),
		Name:        entry.GetName(),
		Owner:       entry.GetOwner(),
		Path:        entry.GetPath(),
		Permissions: entry.GetPermissions(),
		Size:        entry.GetSize(),
		Target:      toTarget(entry.SymlinkTarget),
		Type:        toType(entry.GetType()),
	}
}

func toTarget(target *string) *api.VolumeStatTarget {
	if target == nil {
		return nil
	}

	v := *target
	st := api.VolumeStatTarget(v)

	return &st
}

func toType(getType orchestrator.FileType) string {
	switch getType {
	case orchestrator.FileType_FILE_TYPE_DIRECTORY:
		return "directory"
	case orchestrator.FileType_FILE_TYPE_FILE:
		return "file"
	default:
		return "unknown"
	}
}
