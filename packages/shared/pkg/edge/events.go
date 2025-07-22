package edge

import (
	"errors"
	"strconv"
	"time"

	"google.golang.org/grpc/metadata"
)

const (
	eventTypeHeader = "event-type"

	catalogCreateEventType = "sandbox-catalog-create"
	catalogDeleteEventType = "sandbox-catalog-delete"

	sbxIdHeader               = "sandbox-id"
	sbxExecutionIdHeader      = "execution-id"
	sbxOrchestratorIdHeader   = "orchestrator-id"
	sbxMaxLengthInHoursHeader = "sandbox-max-length-in-hours"
	sbxStartTimeHeader        = "sandbox-start-time"
)

var (
	ErrSandboxCreateEventRequiredFieldsMissing = errors.New("required fields missing for sandbox create event")
	ErrSandboxDeleteEventRequiredFieldsMissing = errors.New("required fields missing for sandbox delete event")

	ErrSandboxCreationParse = errors.New("failed to parse sandbox creation event metadata")
	ErrSandboxLifetimeParse = errors.New("failed to parse sandbox max lifetime event metadata")
)

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
			eventTypeHeader: catalogCreateEventType,

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
			eventTypeHeader: catalogDeleteEventType,

			sbxIdHeader:          e.SandboxID,
			sbxExecutionIdHeader: e.ExecutionID,
		},
	)
}

func HandleSandboxCatalogCreateEvent(md metadata.MD) (e *SandboxCatalogCreateEvent, err error) {
	v, f := getMetadataValue(md, eventTypeHeader)
	if !f || v != catalogCreateEventType {
		return nil, nil
	}

	sandboxID, found := getMetadataValue(md, sbxIdHeader)
	if !found {
		return nil, ErrSandboxCreateEventRequiredFieldsMissing
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, ErrSandboxCreateEventRequiredFieldsMissing
	}

	orchestratorID, found := getMetadataValue(md, sbxOrchestratorIdHeader)
	if !found {
		return nil, ErrSandboxCreateEventRequiredFieldsMissing
	}

	maxLengthInHoursStr, found := getMetadataValue(md, sbxMaxLengthInHoursHeader)
	if !found {
		return nil, ErrSandboxCreateEventRequiredFieldsMissing
	}

	maxLengthInHours, err := strconv.Atoi(maxLengthInHoursStr)
	if err != nil {
		return nil, ErrSandboxLifetimeParse
	}

	sandboxStartTimeStr, found := getMetadataValue(md, sbxStartTimeHeader)
	if !found {
		return nil, ErrSandboxCreateEventRequiredFieldsMissing
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

func HandleSandboxCatalogDeleteEvent(md metadata.MD) (e *SandboxCatalogDeleteEvent, err error) {
	v, f := getMetadataValue(md, eventTypeHeader)
	if !f || v != catalogDeleteEventType {
		return nil, nil
	}

	sandboxID, found := getMetadataValue(md, sbxIdHeader)
	if !found {
		return nil, ErrSandboxDeleteEventRequiredFieldsMissing
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, ErrSandboxDeleteEventRequiredFieldsMissing
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
