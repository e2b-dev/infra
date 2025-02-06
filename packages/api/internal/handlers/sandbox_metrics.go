package handlers

import (
	"context"
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

const defaultLimit = 100

func (a *APIStore) getSandboxesSandboxIDMetrics(
	ctx context.Context,
	sandboxID string,
	teamID string,
	limit int,
	duration time.Duration,
) ([]api.SandboxMetric, error) {
	sandboxID = utils.ShortID(sandboxID)

	end := time.Now()
	start := end.Add(-duration)

	// Sanitize ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	id := strings.ReplaceAll(sandboxID, "`", "")

	// equivalent CLI query:
	// logcli query '{source="logs-collector", service="envd", teamID="65d165ab-69f6-4b5c-9165-6b93cd341503", sandboxID="izuhqjlfabd8ataeixrtl", category="metrics"}' --from="2025-01-19T10:00:00Z"
	query := fmt.Sprintf(
		"{source=\"logs-collector\", service=\"envd\", teamID=`%s`, sandboxID=`%s`, category=\"metrics\"}", teamID, id)

	res, err := a.lokiClient.QueryRange(query, limit, start, end, logproto.BACKWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		errMsg := fmt.Errorf("error when returning metrics for sandbox: %w", err)
		return nil, errMsg
	}

	if res.Data.Result.Type() != loghttp.ResultTypeStream {
		errMsg := fmt.Errorf("unexpected value type %T", res.Data.Result.Type())
		return nil, errMsg
	}

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

	return metrics, nil
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
		telemetry.ReportCriticalError(ctx, err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error returning metrics for sandbox '%s'", sandboxID))
		return
	}

	c.JSON(http.StatusOK, metrics)
	return
}
