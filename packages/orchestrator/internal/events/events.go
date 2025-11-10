package events

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/events"
)

type EventsService struct {
	deliveryTargets []events.Delivery[events.SandboxEvent]
}

type EventFieldMissingError struct {
	fieldName string
}

func (e *EventFieldMissingError) Error() string {
	return fmt.Sprintf("missing required event field: %s", e.fieldName)
}

func NewEventsService(deliveryTargets []events.Delivery[events.SandboxEvent]) *EventsService {
	return &EventsService{
		deliveryTargets: deliveryTargets,
	}
}

func (e *EventsService) Publish(ctx context.Context, teamID uuid.UUID, event events.SandboxEvent) {
	deliveryKey := events.DeliveryKey(teamID)

	err := validateEvent(event)
	if err != nil {
		zap.L().Error("Failed to publish sandbox event due to validation error", zap.Error(err), zap.Any("event", event))

		return
	}

	wg := sync.WaitGroup{}
	for _, target := range e.deliveryTargets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := target.Publish(ctx, deliveryKey, event); err != nil {
				zap.L().Error("Failed to publish sandbox event", zap.Error(err), zap.Any("event", event))
			}
		}()
	}
	wg.Wait()
}

func (e *EventsService) Close(ctx context.Context) error {
	var err error
	for _, target := range e.deliveryTargets {
		closeErr := target.Close(ctx)
		err = errors.Join(err, closeErr)
	}

	return err
}

func validateEvent(event events.SandboxEvent) error {
	if event.Version == "" {
		return &EventFieldMissingError{"version"}
	}

	if event.Type == "" {
		return &EventFieldMissingError{"type"}
	}

	if event.SandboxID == "" {
		return &EventFieldMissingError{"sandbox_id"}
	}

	if event.SandboxTeamID == uuid.Nil {
		return &EventFieldMissingError{"sandbox_team_id"}
	}

	if event.Timestamp.IsZero() {
		return &EventFieldMissingError{"timestamp"}
	}

	return nil
}
