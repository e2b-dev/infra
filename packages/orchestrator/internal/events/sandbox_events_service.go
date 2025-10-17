package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/batcher"
	"github.com/e2b-dev/infra/packages/shared/pkg/events"
	"github.com/e2b-dev/infra/packages/shared/pkg/events/webhooks"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/pubsub"
)

type MissingEventFieldError struct {
	fieldName string
}

func (e *MissingEventFieldError) Error() string {
	return fmt.Sprintf("missing required event field: %s", e.fieldName)
}

// SandboxEventsService manages sandbox events publishing, subscription using PubSub
// as well as persisting using a ClickHouse Batcher
type SandboxEventsService struct {
	featureFlags *featureflags.Client
	pubsub       pubsub.PubSub[events.SandboxEvent, struct{}]
	batcher      batcher.SandboxEventsClickhouseBatcher
	logger       *zap.Logger
}

func NewSandboxEventsService(
	featureFlags *featureflags.Client,
	pubsub pubsub.PubSub[events.SandboxEvent, struct{}],
	batcher batcher.SandboxEventsClickhouseBatcher,
	logger *zap.Logger,
) *SandboxEventsService {
	return &SandboxEventsService{
		featureFlags: featureFlags,
		pubsub:       pubsub,
		batcher:      batcher,
		logger:       logger,
	}
}

// Should be non-blocking no matter what
func (es *SandboxEventsService) HandleEvent(ctx context.Context, event events.SandboxEvent) {
	err := validateEvent(event)
	if err != nil {
		es.logger.Error("error validating sandbox event", zap.Error(err))

		return
	}

	// Create a new context without cancel, so we can pass it to the goroutines
	// and not worry about the parent context being cancelled.
	// This is important because we want to ensure that the goroutines are not cancelled
	// when the parent context is cancelled.
	childCtx := context.WithoutCancel(ctx)

	go es.handlePubSubEvent(childCtx, event)
	go es.handleClickhouseBatcherEvent(childCtx, event)
}

func (es *SandboxEventsService) handlePubSubEvent(ctx context.Context, event events.SandboxEvent) {
	sandboxEventsPublishFlag, flagErr := es.featureFlags.BoolFlag(
		ctx, featureflags.SandboxEventsPublishFlagName, featureflags.SandboxContext(event.SandboxID))
	if flagErr != nil {
		es.logger.Error("soft failing during sandbox events publish feature flag receive", zap.Error(flagErr))
	}
	if sandboxEventsPublishFlag {
		shouldPublish, err := es.pubsub.ShouldPublish(ctx, webhooks.DeriveKey(event.SandboxTeamID))
		if err != nil {
			es.logger.Error("error checking if sandbox should publish", zap.Error(err))

			return
		}

		if !shouldPublish {
			return
		}

		es.logger.Debug("PubSub should publish for sandbox event lifecycle",
			zap.String("sandbox_id", event.SandboxID),
			zap.String("team_id", event.SandboxTeamID.String()),
		)

		err = es.pubsub.Publish(ctx, event)
		if err != nil {
			es.logger.Error("error publishing sandbox event", zap.Error(err))
		}
	}
}

func (es *SandboxEventsService) Close(ctx context.Context) error {
	var errs []error
	if err := es.batcher.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to close batcher: %w", err))
	}

	if err := es.pubsub.Close(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to close pubsub: %w", err))
	}

	return errors.Join(errs...)
}

func (es *SandboxEventsService) handleClickhouseBatcherEvent(ctx context.Context, event events.SandboxEvent) {
	sandboxLifeCycleEventsWriteFlag, flagErr := es.featureFlags.BoolFlag(ctx, featureflags.SandboxLifeCycleEventsWriteFlagName, featureflags.SandboxContext(event.SandboxID))
	if flagErr != nil {
		es.logger.Error("soft failing during sandbox lifecycle events write feature flag receive", zap.Error(flagErr))
	}

	eventData := ""
	eventDataJson, err := json.Marshal(event.EventData)
	if err != nil {
		es.logger.Error("error marshalling sandbox event data", zap.Error(err))
	} else {
		eventData = string(eventDataJson)
	}

	if sandboxLifeCycleEventsWriteFlag {
		err := es.batcher.Push(clickhouse.SandboxEvent{
			Type:      event.Type,
			Version:   event.Version,
			Timestamp: event.Timestamp,

			EventCategory: event.EventCategory,
			EventLabel:    event.EventLabel,
			EventData:     sql.NullString{String: eventData, Valid: eventData != ""},

			SandboxID:          event.SandboxID,
			SandboxTemplateID:  event.SandboxTemplateID,
			SandboxBuildID:     event.SandboxBuildID,
			SandboxTeamID:      event.SandboxTeamID,
			SandboxExecutionID: event.SandboxExecutionID,
		})
		if err != nil {
			es.logger.Error("error inserting sandbox event", zap.String("event", event.Type), zap.Error(err))
		}
	}
}

func validateEvent(event events.SandboxEvent) error {
	if event.Version == "" {
		return &MissingEventFieldError{"version"}
	}

	if event.Type == "" {
		return &MissingEventFieldError{"type"}
	}

	if event.SandboxID == "" {
		return &MissingEventFieldError{"sandbox_id"}
	}

	if event.SandboxTeamID == uuid.Nil {
		return &MissingEventFieldError{"sandbox_team_id"}
	}

	if event.Timestamp.IsZero() {
		return &MissingEventFieldError{"timestamp"}
	}

	return nil
}
