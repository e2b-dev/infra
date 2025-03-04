package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultLimit = 100

func (a *APIStore) getSandboxesSandboxIDMetrics(
	ctx context.Context,
	sandboxID string,
	teamID string,
	limit int,
	duration time.Duration,
) ([]api.SandboxMetric, error) {
	end := time.Now().UTC()
	start := end.Add(-duration)

	metrics, err := a.clickhouseStore.QueryMetrics(ctx, sandboxID, teamID, start.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("error when returning metrics for sandbox: %w", err)
	}

	// XXX avoid this conversion to be more efficient
	apiMetrics := make([]api.SandboxMetric, len(metrics))
	for i, m := range metrics {
		apiMetrics[i] = api.SandboxMetric{
			Timestamp:   m.Timestamp,
			CpuUsedPct:  m.CPUUsedPercent,
			CpuCount:    int32(m.CPUCount),
			MemTotalMiB: int64(m.MemTotalMiB),
			MemUsedMiB:  int64(m.MemUsedMiB),
		}
	}

	return apiMetrics, nil
}

func (a *APIStore) GetSandboxesSandboxIDMetrics(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	teamID := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team.ID.String()

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.String("team.id", teamID),
	)

	metrics, err := a.getSandboxesSandboxIDMetrics(ctx, sandboxID, teamID, defaultLimit, oldestLogsLimit)
	if err != nil {
		zap.L().Error("Error returning metrics for sandbox",
			zap.Error(err),
			zap.String("sandboxID", sandboxID),
		)
		telemetry.ReportCriticalError(ctx, err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandbox '%s'", sandboxID))

		return
	}

	c.JSON(http.StatusOK, metrics)
}
