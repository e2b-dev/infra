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
	e.Version = StructureVersionV1

	if e.EventCategory == SandboxCreatedEventPair.LegacyCategory && e.EventLabel == SandboxCreatedEventPair.LegacyLabel {
		e.Type = SandboxCreatedEventPair.Type
	} else if e.EventCategory == SandboxPausedEventPair.LegacyCategory && e.EventLabel == SandboxPausedEventPair.LegacyLabel {
		e.Type = SandboxPausedEventPair.Type
	} else if e.EventCategory == SandboxResumedEventPair.LegacyCategory && e.EventLabel == SandboxResumedEventPair.LegacyLabel {
		e.Type = SandboxResumedEventPair.Type
	} else if e.EventCategory == SandboxUpdatedEventPair.LegacyCategory && e.EventLabel == SandboxUpdatedEventPair.LegacyLabel {
		e.Type = SandboxUpdatedEventPair.Type
	} else if e.EventCategory == SandboxKilledEventPair.LegacyCategory && e.EventLabel == SandboxKilledEventPair.LegacyLabel {
		e.Type = SandboxKilledEventPair.Type
	}

	return e
}
