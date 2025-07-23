package edge

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/metadata"
)

const (
	EventTypeHeader = "event-type"

	CatalogCreateEventType = "sandbox-catalog-create"
	CatalogDeleteEventType = "sandbox-catalog-delete"

	sbxIdHeader               = "sandbox-id"
	sbxExecutionIdHeader      = "execution-id"
	sbxOrchestratorIdHeader   = "orchestrator-id"
	sbxMaxLengthInHoursHeader = "sandbox-max-length-in-hours"
	sbxStartTimeHeader        = "sandbox-start-time"
)

var (
	ErrSandboxCreationParse = errors.New("failed to parse sandbox creation event metadata")
	ErrSandboxLifetimeParse = errors.New("failed to parse sandbox max lifetime event metadata")
)

type SandboxEventFieldMissing struct {
	eventName string
	fieldName string
}

func (e SandboxEventFieldMissing) Error() string {
	return fmt.Sprintf("missing required field (%s) in sandbox create event %s", e.fieldName, e.eventName)
}

type SandboxCatalogCreateEvent struct {
	SandboxID               string
	ExecutionID             string
	OrchestratorID          string
	SandboxMaxLengthInHours int64
	SandboxStartTime        time.Time // Formatted as RFC3339 (ISO 8601)
}

type SandboxCatalogDeleteEvent struct {
	SandboxID   string
	ExecutionID string
}

func SerializeSandboxCatalogCreateEvent(e SandboxCatalogCreateEvent) metadata.MD {
	return metadata.New(
		map[string]string{
			EventTypeHeader: CatalogCreateEventType,

			sbxIdHeader:               e.SandboxID,
			sbxExecutionIdHeader:      e.ExecutionID,
			sbxOrchestratorIdHeader:   e.OrchestratorID,
			sbxStartTimeHeader:        e.SandboxStartTime.Format(time.RFC3339),
			sbxMaxLengthInHoursHeader: strconv.Itoa(int(e.SandboxMaxLengthInHours)),
		},
	)
}

func SerializeSandboxCatalogDeleteEvent(e SandboxCatalogDeleteEvent) metadata.MD {
	return metadata.New(
		map[string]string{
			EventTypeHeader: CatalogDeleteEventType,

			sbxIdHeader:          e.SandboxID,
			sbxExecutionIdHeader: e.ExecutionID,
		},
	)
}

func ParseSandboxCatalogCreateEvent(md metadata.MD) (e *SandboxCatalogCreateEvent, err error) {
	sandboxID, found := getMetadataValue(md, sbxIdHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogCreateEventType, fieldName: sbxIdHeader}
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogCreateEventType, fieldName: sbxExecutionIdHeader}
	}

	orchestratorID, found := getMetadataValue(md, sbxOrchestratorIdHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogCreateEventType, fieldName: sbxOrchestratorIdHeader}
	}

	maxLengthInHoursStr, found := getMetadataValue(md, sbxMaxLengthInHoursHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogCreateEventType, fieldName: sbxMaxLengthInHoursHeader}
	}

	maxLengthInHours, err := strconv.Atoi(maxLengthInHoursStr)
	if err != nil {
		return nil, ErrSandboxLifetimeParse
	}

	sandboxStartTimeStr, found := getMetadataValue(md, sbxStartTimeHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogCreateEventType, fieldName: sbxStartTimeHeader}
	}

	sandboxStartTime, err := time.Parse(time.RFC3339, sandboxStartTimeStr)
	if err != nil {
		return nil, ErrSandboxCreationParse
	}

	return &SandboxCatalogCreateEvent{
		SandboxID:               sandboxID,
		ExecutionID:             executionID,
		OrchestratorID:          orchestratorID,
		SandboxMaxLengthInHours: int64(maxLengthInHours),
		SandboxStartTime:        sandboxStartTime,
	}, nil
}

func ParseSandboxCatalogDeleteEvent(md metadata.MD) (e *SandboxCatalogDeleteEvent, err error) {
	sandboxID, found := getMetadataValue(md, sbxIdHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogDeleteEventType, fieldName: sbxIdHeader}
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, SandboxEventFieldMissing{eventName: CatalogDeleteEventType, fieldName: sbxExecutionIdHeader}
	}

	return &SandboxCatalogDeleteEvent{
		SandboxID:   sandboxID,
		ExecutionID: executionID,
	}, nil
}

func getMetadataValue(md metadata.MD, key string) (value string, found bool) {
	if values, ok := md[key]; ok && len(values) > 0 {
		return values[0], true
	}
	return "", false
}
