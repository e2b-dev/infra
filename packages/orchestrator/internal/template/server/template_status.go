package server

import (
	"context"

	"github.com/pkg/errors"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const maxLogEntriesPerRequest = int32(100)

func (s *ServerStore) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest) (*template_manager.TemplateBuildStatusResponse, error) {
	_, ctxSpan := tracer.Start(ctx, "template-build-status-request")
	defer ctxSpan.End()

	buildInfo, err := s.buildCache.Get(in.GetBuildID())
	if err != nil {
		return nil, errors.Wrap(err, "error while getting build info, maybe already expired")
	}

	limit := maxLogEntriesPerRequest
	if in.Limit != nil && in.GetLimit() < maxLogEntriesPerRequest {
		limit = in.GetLimit()
	}

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

		if int32(len(logEntries)) >= limit {
			break
		}

		logEntries = append(logEntries, entry)
	}

	result := buildInfo.GetResult()
	if result == nil {
		return &template_manager.TemplateBuildStatusResponse{
			Status:     template_manager.TemplateBuildState_Building,
			Reason:     nil,
			Metadata:   nil,
			LogEntries: logEntries,
		}, nil
	}

	return &template_manager.TemplateBuildStatusResponse{
		Status:     result.Status,
		Reason:     result.Reason,
		Metadata:   result.Metadata,
		LogEntries: logEntries,
	}, nil
}
