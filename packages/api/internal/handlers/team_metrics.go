package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultTimeRange = 7 * 24 * time.Hour // 7 days

func (a *APIStore) GetTeamsTeamIDMetrics(c *gin.Context, teamID string, params api.GetTeamsTeamIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := a.Tracer.Start(ctx, "sandbox-metrics")
	defer span.End()

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	if teamID != team.ID.String() {
		telemetry.ReportError(ctx, "team ids mismatch", fmt.Errorf("you (%s) are not authorized to access this team's (%s) metrics", team.ID, teamID), telemetry.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("You (%s) are not authorized to access this team's (%s) metrics", team.ID, teamID))

		return
	}

	metricsReadFlag, err := a.featureFlags.BoolFlag(featureflags.MetricsReadFlagName, team.ID.String())
	if err != nil {
		zap.L().Warn("error getting metrics read feature flag, soft failing", zap.Error(err))
	}

	if !metricsReadFlag {
		zap.L().Debug("sandbox metrics read feature flag is disabled")
		// If we are not reading from ClickHouse, we can return an empty map
		// This is here just to have the possibility to turn off ClickHouse metrics reading

		c.JSON(http.StatusOK, []api.TeamMetric{})
		return
	}

	// Default time range is the last 7 days
	start, end := time.Now().Add(-defaultTimeRange), time.Now()
	if params.Start != nil {
		start = time.Unix(*params.Start, 0)
	}

	if params.End != nil {
		end = time.Unix(*params.End, 0)
	}

	// Validate time range parameters
	if start.After(end) {
		telemetry.ReportError(ctx, "start after end", fmt.Errorf("start time (%s) cannot be after end time (%s)", start, end), telemetry.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusBadRequest, "start time cannot be after end time")
		return
	}

	step := calculateStep(start, end)

	metrics, err := a.clickhouseStore.QueryTeamMetrics(ctx, teamID, start, end, step)
	if err != nil {
		telemetry.ReportError(ctx, "error fetching team metrics", err, telemetry.WithTeamID(team.ID.String()))
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error querying team metrics: %s", err))
		return
	}

	apiMetrics := make([]api.TeamMetric, len(metrics))
	for i, m := range metrics {
		apiMetrics[i] = api.TeamMetric{
			Timestamp:           m.Timestamp,
			ConcurrentSandboxes: int32(m.ConcurrentSandboxes),
			SandboxStartRate:    float32(m.SandboxStartedRate),
		}
	}

	c.JSON(http.StatusOK, apiMetrics)
}
