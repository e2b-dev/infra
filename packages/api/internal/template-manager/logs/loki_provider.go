package logs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/logproto"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

const (
	templateBuildLogsLimit       = 1_000
	templateBuildOldestLogsLimit = 24 * time.Hour // 1 day
)

type LokiProvider struct {
	LokiClient *client.DefaultClient
}

func (l *LokiProvider) GetLogs(ctx context.Context, templateID string, buildID string, offset int32, level *logs.LogLevel) ([]logs.LogEntry, error) {
	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateID, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildID, templateIdSanitized)

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)

	res, err := l.LokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err != nil {
		return nil, fmt.Errorf("error when querying loki for template build logs: %w", err)
	}

	lm, err := logs.LokiResponseMapper(res, offset, level)
	if err != nil {
		return nil, fmt.Errorf("error when mapping loki response: %w", err)
	}

	return lm, nil
}
