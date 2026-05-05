package edge

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/grpc/metadata"

	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

const (
	EventTypeHeader = "event-type"

	CatalogCreateEventType = "sandbox-catalog-create"
	CatalogDeleteEventType = "sandbox-catalog-delete"

	sbxIdHeader               = "sandbox-id"
	sbxTeamIdHeader           = "team-id"
	sbxExecutionIdHeader      = "execution-id"
	sbxOrchestratorIdHeader   = "orchestrator-id"
	sbxEndTimeHeader          = "sandbox-end-time"
	sbxMaxLengthInHoursHeader = "sandbox-max-length-in-hours"
	sbxStartTimeHeader        = "sandbox-start-time"
	sbxTrafficKeepaliveHeader = "traffic-keepalive"
)

var (
	ErrSandboxCreationParse = errors.New("failed to parse sandbox creation event metadata")
	ErrSandboxLifetimeParse = errors.New("failed to parse sandbox max lifetime event metadata")
)

type SandboxEventFieldMissingError struct {
	eventName string
	fieldName string
}

func (e SandboxEventFieldMissingError) Error() string {
	return fmt.Sprintf("missing required field (%s) in sandbox create event %s", e.fieldName, e.eventName)
}

type SandboxCatalogCreateEvent struct {
	SandboxID               string
	TeamID                  string
	ExecutionID             string
	OrchestratorID          string
	SandboxMaxLengthInHours int64
	SandboxStartTime        time.Time // Formatted as RFC3339 (ISO 8601)
	SandboxEndTime          time.Time // Formatted as RFC3339 (ISO 8601)
	Keepalive               *catalog.Keepalive
}

type SandboxCatalogDeleteEvent struct {
	SandboxID   string
	ExecutionID string
}

func SerializeSandboxCatalogCreateEvent(e SandboxCatalogCreateEvent) metadata.MD {
	values := map[string]string{
		EventTypeHeader: CatalogCreateEventType,

		sbxIdHeader:               e.SandboxID,
		sbxTeamIdHeader:           e.TeamID,
		sbxExecutionIdHeader:      e.ExecutionID,
		sbxOrchestratorIdHeader:   e.OrchestratorID,
		sbxEndTimeHeader:          e.SandboxEndTime.Format(time.RFC3339),
		sbxStartTimeHeader:        e.SandboxStartTime.Format(time.RFC3339),
		sbxMaxLengthInHoursHeader: strconv.Itoa(int(e.SandboxMaxLengthInHours)),
	}
	if e.Keepalive != nil && e.Keepalive.Traffic != nil && e.Keepalive.Traffic.Enabled {
		values[sbxTrafficKeepaliveHeader] = strconv.FormatBool(true)
	}

	return metadata.New(values)
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
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: sbxIdHeader}
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: sbxExecutionIdHeader}
	}

	teamID, _ := getMetadataValue(md, sbxTeamIdHeader)

	orchestratorID, found := getMetadataValue(md, sbxOrchestratorIdHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: sbxOrchestratorIdHeader}
	}

	maxLengthInHoursStr, found := getMetadataValue(md, sbxMaxLengthInHoursHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: sbxMaxLengthInHoursHeader}
	}

	maxLengthInHours, err := strconv.Atoi(maxLengthInHoursStr)
	if err != nil {
		return nil, ErrSandboxLifetimeParse
	}

	sandboxStartTimeStr, found := getMetadataValue(md, sbxStartTimeHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: sbxStartTimeHeader}
	}

	sandboxStartTime, err := time.Parse(time.RFC3339, sandboxStartTimeStr)
	if err != nil {
		return nil, ErrSandboxCreationParse
	}

	var sandboxEndTime time.Time
	sandboxEndTimeStr, found := getMetadataValue(md, sbxEndTimeHeader)
	if found {
		sandboxEndTime, err = time.Parse(time.RFC3339, sandboxEndTimeStr)
		if err != nil {
			return nil, ErrSandboxCreationParse
		}
	}

	var keepalive *catalog.Keepalive
	trafficKeepaliveStr, found := getMetadataValue(md, sbxTrafficKeepaliveHeader)
	if found {
		trafficKeepalive, err := strconv.ParseBool(trafficKeepaliveStr)
		if err != nil {
			return nil, ErrSandboxCreationParse
		}

		if trafficKeepalive {
			keepalive = &catalog.Keepalive{
				Traffic: &catalog.TrafficKeepalive{
					Enabled: true,
				},
			}
		}
	}

	return &SandboxCatalogCreateEvent{
		SandboxID:               sandboxID,
		TeamID:                  teamID,
		ExecutionID:             executionID,
		OrchestratorID:          orchestratorID,
		SandboxMaxLengthInHours: int64(maxLengthInHours),
		SandboxStartTime:        sandboxStartTime,
		SandboxEndTime:          sandboxEndTime,
		Keepalive:               keepalive,
	}, nil
}

func ParseSandboxCatalogDeleteEvent(md metadata.MD) (e *SandboxCatalogDeleteEvent, err error) {
	sandboxID, found := getMetadataValue(md, sbxIdHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogDeleteEventType, fieldName: sbxIdHeader}
	}

	executionID, found := getMetadataValue(md, sbxExecutionIdHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogDeleteEventType, fieldName: sbxExecutionIdHeader}
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
