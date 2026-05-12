package edge

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/grpc/metadata"
)

const (
	EventTypeHeader = "event-type"

	CatalogCreateEventType = "sandbox-catalog-create"
	CatalogDeleteEventType = "sandbox-catalog-delete"

	SandboxIDHeader               = "sandbox-id"
	SandboxTeamIDHeader           = "team-id"
	SandboxExecutionIDHeader      = "execution-id"
	SandboxOrchestratorIDHeader   = "orchestrator-id"
	SandboxOrchestratorIPHeader   = "orchestrator-ip"
	SandboxMaxLengthInHoursHeader = "sandbox-max-length-in-hours"
	SandboxTrafficKeepaliveHeader = "traffic-keepalive"
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
	OrchestratorIP          string
	SandboxMaxLengthInHours int64
	TrafficKeepalive        bool
}

type SandboxCatalogDeleteEvent struct {
	SandboxID   string
	ExecutionID string
}

func SerializeSandboxCatalogCreateEvent(e SandboxCatalogCreateEvent) metadata.MD {
	values := map[string]string{
		EventTypeHeader: CatalogCreateEventType,

		SandboxIDHeader:               e.SandboxID,
		SandboxTeamIDHeader:           e.TeamID,
		SandboxExecutionIDHeader:      e.ExecutionID,
		SandboxOrchestratorIDHeader:   e.OrchestratorID,
		SandboxOrchestratorIPHeader:   e.OrchestratorIP,
		SandboxMaxLengthInHoursHeader: strconv.Itoa(int(e.SandboxMaxLengthInHours)),
	}
	if e.TrafficKeepalive {
		values[SandboxTrafficKeepaliveHeader] = strconv.FormatBool(true)
	}

	return metadata.New(values)
}

func SerializeSandboxCatalogDeleteEvent(e SandboxCatalogDeleteEvent) metadata.MD {
	return metadata.New(
		map[string]string{
			EventTypeHeader: CatalogDeleteEventType,

			SandboxIDHeader:          e.SandboxID,
			SandboxExecutionIDHeader: e.ExecutionID,
		},
	)
}

func ParseSandboxCatalogCreateEvent(md metadata.MD) (e *SandboxCatalogCreateEvent, err error) {
	sandboxID, found := getMetadataValue(md, SandboxIDHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: SandboxIDHeader}
	}

	executionID, found := getMetadataValue(md, SandboxExecutionIDHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: SandboxExecutionIDHeader}
	}

	teamID, _ := getMetadataValue(md, SandboxTeamIDHeader)

	orchestratorID, found := getMetadataValue(md, SandboxOrchestratorIDHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: SandboxOrchestratorIDHeader}
	}

	orchestratorIP, _ := getMetadataValue(md, SandboxOrchestratorIPHeader)

	maxLengthInHoursStr, found := getMetadataValue(md, SandboxMaxLengthInHoursHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogCreateEventType, fieldName: SandboxMaxLengthInHoursHeader}
	}

	maxLengthInHours, err := strconv.Atoi(maxLengthInHoursStr)
	if err != nil {
		return nil, ErrSandboxLifetimeParse
	}

	var trafficKeepalive bool
	trafficKeepaliveStr, found := getMetadataValue(md, SandboxTrafficKeepaliveHeader)
	if found {
		trafficKeepalive, err = strconv.ParseBool(trafficKeepaliveStr)
		if err != nil {
			return nil, ErrSandboxCreationParse
		}
	}
	// Missing header means false; create events only serialize true values.

	return &SandboxCatalogCreateEvent{
		SandboxID:               sandboxID,
		TeamID:                  teamID,
		ExecutionID:             executionID,
		OrchestratorID:          orchestratorID,
		OrchestratorIP:          orchestratorIP,
		SandboxMaxLengthInHours: int64(maxLengthInHours),
		TrafficKeepalive:        trafficKeepalive,
	}, nil
}

func ParseSandboxCatalogDeleteEvent(md metadata.MD) (e *SandboxCatalogDeleteEvent, err error) {
	sandboxID, found := getMetadataValue(md, SandboxIDHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogDeleteEventType, fieldName: SandboxIDHeader}
	}

	executionID, found := getMetadataValue(md, SandboxExecutionIDHeader)
	if !found {
		return nil, SandboxEventFieldMissingError{eventName: CatalogDeleteEventType, fieldName: SandboxExecutionIDHeader}
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
