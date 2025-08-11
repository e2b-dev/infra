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
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (a *APIStore) GetTeamsTeamIDMetrics(c *gin.Context, teamID string, params api.GetTeamsTeamIDMetricsParams) {
	ctx := c.Request.Context()
	ctx, span := a.Tracer.Start(ctx, "sandbox-metrics")
	defer span.End()

	team := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team

	if teamID != team.ID.String() {
		zap.L().Warn("user tried to access metrics for a team they are not authorized to access", logger.WithTeamID(team.ID.String()), zap.String("requested_team_id", teamID))
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
	start, end := time.Now().Add(-7*24*time.Hour), time.Now()
	if params.Start != nil {
		start = time.Unix(*params.Start, 0)
	}

	if params.End != nil {
		end = time.Unix(*params.End, 0)
	}

	// Validate time range parameters
	if start.After(end) {
		a.sendAPIStoreError(c, http.StatusBadRequest, "start time cannot be after end time")
		return
	}

	var step time.Duration
	duration := end.Sub(start)
	switch {
	case duration < time.Hour:
		step = 5 * time.Second
	case duration < 6*time.Hour:
		step = 30 * time.Second
	case duration < 12*time.Hour:
		step = time.Minute
	case duration < 24*time.Hour:
		step = 2 * time.Minute
	case duration < 7*24*time.Hour:
		step = 5 * time.Minute
	default:
		step = 15 * time.Minute
	}

	metrics, err := a.clickhouseStore.QueryTeamMetrics(ctx, teamID, start, end, step)
	if err != nil {
		zap.L().Error("Error fetching sandbox metrics from ClickHouse",
			logger.WithTeamID(team.ID.String()),
			zap.Error(err),
		)

		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("error querying sandbox metrics: %s", err))
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
