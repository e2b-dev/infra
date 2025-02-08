package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/auth"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetSandboxesSandboxIDMetrics(
	c *gin.Context,
	sandboxID string,
) {
	ctx := c.Request.Context()
	sandboxID = utils.ShortID(sandboxID)

	teamID := c.Value(auth.TeamContextKey).(authcache.AuthTeamInfo).Team.ID

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sandboxID),
		attribute.String("team.id", teamID.String()),
	)

	end := time.Now()
	start := end.Add(-oldestLogsLimit)

	// Sanitize ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	id := strings.ReplaceAll(sandboxID, "`", "")

	// equivalent CLI query:
	// logcli query '{source="logs-collector", service="envd", teamID="65d165ab-69f6-4b5c-9165-6b93cd341503", sandboxID="izuhqjlfabd8ataeixrtl", category="metrics"}' --from="2025-01-19T10:00:00Z"
	query := fmt.Sprintf(
		"{source=\"logs-collector\", service=\"envd\", teamID=`%s`, sandboxID=`%s`, category=\"metrics\"}", teamID.String(), id)

	res, err := a.lokiClient.QueryRange(query, 100, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning metrics for sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error returning metrics for sandbox '%s'", sandboxID))

		return
	}

	switch res.Data.Result.Type() {
	case loghttp.ResultTypeStream:
		value := res.Data.Result.(loghttp.Streams)

		metrics := make([]api.SandboxMetric, 0)

		for _, stream := range value {
			for _, entry := range stream.Entries {

				var metric struct {
					Timestamp   time.Time `json:"timestamp"`
					CPUUsedPct  float32   `json:"cpuUsedPct"`
					CPUCount    int32     `json:"cpuCount"`
					MemTotalMiB int64     `json:"memTotalMiB"`
					MemUsedMiB  int64     `json:"memUsedMiB"`
				}

				err := json.Unmarshal([]byte(entry.Line), &metric)
				if err != nil {
					telemetry.ReportCriticalError(ctx, fmt.Errorf("failed to unmarshal metric: %w", err))
					continue
				}
				metrics = append(metrics, api.SandboxMetric{
					Timestamp:   metric.Timestamp,
					CpuUsedPct:  metric.CPUUsedPct,
					CpuCount:    metric.CPUCount,
					MemTotalMiB: metric.MemTotalMiB,
					MemUsedMiB:  metric.MemUsedMiB,
				})
			}
		}

		// Sort metrics by timestamp (they are returned by the time they arrived in Loki)
		slices.SortFunc(metrics, func(a, b api.SandboxMetric) int {
			return a.Timestamp.Compare(b.Timestamp)
		})

		c.JSON(http.StatusOK, metrics)

	default:
		errMsg := fmt.Errorf("unexpected value type %T", res.Data.Result.Type())
		telemetry.ReportCriticalError(ctx, errMsg)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandbox '%s", sandboxID))

		return
	}
}
