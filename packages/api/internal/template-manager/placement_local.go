package template_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	tempaltemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	loki "github.com/grafana/loki/pkg/logcli/client"
	"github.com/grafana/loki/pkg/loghttp"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"strings"
	"time"
)

func NewLocalBuildPlacement(client *GRPCClient, lokiClient *loki.DefaultClient) *LocalBuildPlacement {
	return &LocalBuildPlacement{
		client: client,
	}
}

func (l *LocalBuildPlacement) GetStatus(ctx context.Context, buildId string, templateId string) (*tempaltemanagergrpc.TemplateBuildStatusResponse, error) {
	status, err := l.client.TemplateClient.TemplateBuildStatus(ctx, &tempaltemanagergrpc.TemplateStatusRequest{TemplateID: templateId, BuildID: buildId})
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return nil, errors.Wrap(err, "context deadline exceeded")
	} else if err != nil { // retry only on context deadline exceeded
		zap.L().Error("terminal error when polling build status", zap.Error(err))
		return nil, newTerminalError(err)
	}

	if status == nil {
		return nil, errors.New("nil status") // this should never happen
	}

	return status, nil
}

func (l *LocalBuildPlacement) StartBuild(ctx context.Context, req *tempaltemanagergrpc.TemplateCreateRequest) error {
	_, err := l.client.TemplateClient.TemplateCreate(ctx, req)
	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", req.Template.TemplateID, err)
	}

	return nil
}

func (l *LocalBuildPlacement) DeleteBuild(ctx context.Context, buildId string, templateId string) error {
	_, err := l.client.TemplateClient.TemplateBuildDelete(
		ctx, &tempaltemanagergrpc.TemplateBuildDeleteRequest{
			BuildID:    buildId,
			TemplateID: templateId,
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to delete env build '%s': %w", buildId, err)
	}

	return nil
}

func (l *LocalBuildPlacement) GetLogs(ctx context.Context, buildId string, templateId string, offset *int32) (*[]string, error) {
	// Sanitize env ID
	// https://grafana.com/blog/2021/01/05/how-to-escape-special-characters-with-lokis-logql/
	templateIdSanitized := strings.ReplaceAll(templateId, "`", "")
	query := fmt.Sprintf("{service=\"template-manager\", buildID=\"%s\", envID=`%s`}", buildId, templateIdSanitized)

	end := time.Now()
	start := end.Add(-templateBuildOldestLogsLimit)
	logs := make([]string, 0)

	res, err := l.lokiClient.QueryRange(query, templateBuildLogsLimit, start, end, logproto.FORWARD, time.Duration(0), time.Duration(0), true)
	if err == nil {
		logsCrawled := 0
		logsOffset := 0
		if offset != nil {
			logsOffset = int(*offset)
		}

		if res.Data.Result.Type() != loghttp.ResultTypeStream {
			zap.L().Error("unexpected value type received from loki query fetch", zap.String("type", string(res.Data.Result.Type())))
			return nil, fmt.Errorf("unexpected value type received from loki query fetch")
		}

		for _, stream := range res.Data.Result.(loghttp.Streams) {
			for _, entry := range stream.Entries {
				logsCrawled++

				// loki does not support offset pagination, so we need to skip logs manually
				if logsCrawled <= logsOffset {
					continue
				}

				line := make(map[string]interface{})
				err := json.Unmarshal([]byte(entry.Line), &line)
				if err != nil {
					zap.L().Error("error parsing log line", zap.Error(err), logger.WithBuildID(buildId), zap.String("line", entry.Line))
				}

				logs = append(logs, line["message"].(string))
			}
		}
	} else {
		telemetry.ReportError(ctx, "error when returning logs for template build", err)
		zap.L().Error("error when returning logs for template build", zap.Error(err), logger.WithBuildID(buildId))
	}

	return &logs, nil
}
