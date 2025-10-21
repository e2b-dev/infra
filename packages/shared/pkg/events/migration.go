package events

// Deprecated: use only for already existing events for during migration period
type SandboxEventType struct {
	Type           string
	LegacyCategory string
	LegacyLabel    string
}

var SandboxCreatedEventPair = SandboxEventType{
	Type:           SandboxCreatedEvent,
	LegacyCategory: "lifecycle",
	LegacyLabel:    "create",
}

var SandboxPausedEventPair = SandboxEventType{
	Type:           SandboxPausedEvent,
	LegacyCategory: "lifecycle",
	LegacyLabel:    "pause",
}

var SandboxResumedEventPair = SandboxEventType{
	Type:           SandboxResumedEvent,
	LegacyCategory: "lifecycle",
	LegacyLabel:    "resume",
}

var SandboxUpdatedEventPair = SandboxEventType{
	Type:           SandboxUpdatedEvent,
	LegacyCategory: "lifecycle",
	LegacyLabel:    "update",
}

var SandboxKilledEventPair = SandboxEventType{
	Type:           SandboxKilledEvent,
	LegacyCategory: "lifecycle",
	LegacyLabel:    "kill",
}

// LegacySandboxEventMigrationMapping works for senders back compatibility and converting old event types to new ones
// We will receive old event just with event category and label, so we need to map them to new event types that
// are using new dot namespaced syntax for event names
func LegacySandboxEventMigrationMapping(e SandboxEvent) SandboxEvent {
	switch e.Version {
	case StructureVersionV1:
		// Migrate old event category/label to new event type
		if e.EventCategory == "lifecycle" {
			switch e.EventLabel {
			case "create":
				e.Type = SandboxCreatedEventPair.Type
			case "pause":
				e.Type = SandboxPausedEventPair.Type
			case "resume":
				e.Type = SandboxResumedEventPair.Type
			case "update":
				e.Type = SandboxUpdatedEventPair.Type
			case "kill":
				e.Type = SandboxKilledEventPair.Type
			}
		}
	case StructureVersionV2:
		// Back compatibility for v2 events that might still have legacy fields set
		switch e.Type {
		case SandboxCreatedEvent:
			e.EventCategory = SandboxCreatedEventPair.LegacyCategory
			e.EventLabel = SandboxCreatedEventPair.LegacyLabel
		case SandboxPausedEvent:
			e.EventCategory = SandboxPausedEventPair.LegacyCategory
			e.EventLabel = SandboxPausedEventPair.LegacyLabel
		case SandboxResumedEvent:
			e.EventCategory = SandboxResumedEventPair.LegacyCategory
			e.EventLabel = SandboxResumedEventPair.LegacyLabel
		case SandboxUpdatedEvent:
			e.EventCategory = SandboxUpdatedEventPair.LegacyCategory
			e.EventLabel = SandboxUpdatedEventPair.LegacyLabel
		case SandboxKilledEvent:
			e.EventCategory = SandboxKilledEventPair.LegacyCategory
			e.EventLabel = SandboxKilledEventPair.LegacyLabel
		}
	}

	return e
}
