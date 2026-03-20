package server

import (
	"context"
	"slices"
	"time"

	"github.com/pkg/errors"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

const (
	maxLogEntriesPerRequest = uint32(100)
	defaultTimeRange        = 24 * time.Hour

	defaultDirection = template_manager.LogsDirection_Forward
)

func (s *ServerStore) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest) (*template_manager.TemplateBuildStatusResponse, error) {
	_, span := tracer.Start(ctx, "template-build-status-request")
	defer span.End()

	buildInfo, err := s.buildCache.Get(in.GetBuildID())
	if err != nil {
		return nil, errors.Wrap(err, "error while getting build info, maybe already expired")
	}

	limit := maxLogEntriesPerRequest
	if in.Limit != nil && in.GetLimit() < maxLogEntriesPerRequest {
		limit = in.GetLimit()
	}

	direction := defaultDirection
	if in.GetDirection() == template_manager.LogsDirection_Backward {
		direction = template_manager.LogsDirection_Backward
	}

	start, end := time.Now().Add(-defaultTimeRange), time.Now()
	if s := in.GetStart(); s != nil {
		start = s.AsTime()
	}
	if e := in.GetEnd(); e != nil {
		end = e.AsTime()
	}

	logLines := buildInfo.GetLogs()

	// Keep response ordering aligned with persistent log mapping.
	slices.SortStableFunc(logLines, func(a, b *template_manager.TemplateBuildLogEntry) int {
		if direction == template_manager.LogsDirection_Backward {
			return b.GetTimestamp().AsTime().Compare(a.GetTimestamp().AsTime())
		}

		return a.GetTimestamp().AsTime().Compare(b.GetTimestamp().AsTime())
	})

	logEntries := make([]*template_manager.TemplateBuildLogEntry, 0)
	logsCrawled := int32(0)
	for _, entry := range logLines {
		// Skip entries that are below the specified level
		if entry.GetLevel().Number() < in.GetLevel().Number() {
			continue
		}

		if entry.GetTimestamp().AsTime().Before(start) {
			continue
		}

		if entry.GetTimestamp().AsTime().After(end) {
			continue
		}

		logsCrawled++
		if logsCrawled <= in.GetOffset() {
			continue
		}

		if uint32(len(logEntries)) >= limit {
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
