package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func getSandboxesSandboxIDMetrics(
	ctx context.Context,
	clickhouse clickhouse.Clickhouse,
	sandboxIDs []string,
	teamID string,
) (map[string]api.SandboxMetric, error) {
	metrics, err := clickhouse.QueryLatestMetrics(ctx, sandboxIDs, teamID)
	if err != nil {
		return nil, fmt.Errorf("error querying metrics: %w", err)
	}

	// XXX avoid this conversion to be more efficient
	apiMetrics := make(map[string]api.SandboxMetric)
	for _, m := range metrics {
		apiMetrics[m.SandboxID] = api.SandboxMetric{
			Timestamp:   m.Timestamp,
			CpuUsedPct:  float32(m.CPUUsedPercent),
			CpuCount:    int32(m.CPUCount),
			MemTotalMiB: int64(m.MemTotal / 1024 / 1024), // Convert from bytes to MiB
			MemUsedMiB:  int64(m.MemUsed / 1024 / 1024),  // Convert from bytes to MiB
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
		telemetry.WithSandboxID(sandboxID),
		telemetry.WithTeamID(teamID),
	)

	flagCtx := ldcontext.NewBuilder(featureflags.MetricsReadFlagName).SetString("sandbox_id", sandboxID).Build()
	metricsReadFlag, flagErr := a.featureFlags.Ld.BoolVariation(featureflags.MetricsReadFlagName, flagCtx, featureflags.MetricsReadDefault)
	if flagErr != nil {
		zap.L().Error("soft failing during metrics write feature flag receive", zap.Error(flagErr))
	}

	if !metricsReadFlag {
		zap.L().Debug("sandbox metrics read feature flag is disabled", logger.WithSandboxID(sandboxID))
		// If we are not reading from ClickHouse, we can return an empty slice
		// This is here just to have possibility to turn off ClickHouse metrics reading
		c.JSON(http.StatusOK, []api.SandboxMetric{})
		return
	}

	c.JSON(http.StatusOK, []api.SandboxMetric{})
}
