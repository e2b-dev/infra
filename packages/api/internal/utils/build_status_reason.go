package utils

import (
	"encoding/json"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

// SerializeBuildStatusReason serializes a BuildStatusReason to a JSON string for database storage.
// Returns nil if the reason is nil.
func SerializeBuildStatusReason(reason *api.BuildStatusReason) *string {
	if reason == nil {
		return nil
	}

	serialized, err := json.Marshal(reason)
	if err != nil {
		zap.L().Warn("Failed to serialize build status reason",
			zap.Error(err),
		)
		return nil
	}

	reasonStr := string(serialized)
	return &reasonStr
}

// DeserializeBuildStatusReason deserializes a JSON string from the database to a BuildStatusReason.
// Returns nil if the reason string is empty.
// If JSON parsing fails, returns a BuildStatusReason with the raw string as the message.
func DeserializeBuildStatusReason(reason *string) *api.BuildStatusReason {
	dbReason := ""
	if reason != nil {
		dbReason = *reason
	}

	if dbReason == "" {
		return nil
	}

	var parsedReason *api.BuildStatusReason
	err := json.Unmarshal([]byte(dbReason), &parsedReason)
	if err != nil {
		zap.L().Warn("Failed to parse build status reason from DB",
			zap.String("reason", dbReason),
			zap.Error(err),
		)
		// If parsing fails, we just store the raw reason as a message
		parsedReason = &api.BuildStatusReason{
			Step:    "",
			Message: dbReason,
		}
	}

	return parsedReason
}
