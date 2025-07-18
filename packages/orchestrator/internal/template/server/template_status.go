package server

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (s *ServerStore) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest) (*template_manager.TemplateBuildStatusResponse, error) {
	_, ctxSpan := s.tracer.Start(ctx, "template-build-status-request")
	defer ctxSpan.End()

	buildInfo, err := s.buildCache.Get(in.BuildID)
	if err != nil {
		return nil, errors.Wrap(err, "error while getting build info, maybe already expired")
	}

	logs := make([]string, 0)
	logEntries := make([]*template_manager.TemplateBuildLogEntry, 0)
	logsCrawled := int32(0)
	for _, entry := range buildInfo.GetLogs() {
		// Skip entries that are below the specified level
		if entry.GetLevel().Number() < in.GetLevel().Number() {
			continue
		}

		logsCrawled++
		if logsCrawled <= in.GetOffset() {
			continue
		}

		logEntries = append(logEntries, entry)
		logs = append(logs, fmt.Sprintf("[%s] %s", entry.Timestamp.AsTime().Format(time.RFC3339), entry.Message))
	}

	return &template_manager.TemplateBuildStatusResponse{
		Status:     buildInfo.GetStatus(),
		Reason:     buildInfo.GetReason(),
		Metadata:   buildInfo.GetMetadata(),
		Logs:       logs,
		LogEntries: logEntries,
	}, nil
}
