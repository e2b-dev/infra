package events

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type Delivery[Payload any] interface {
	Publish(ctx context.Context, deliveryKey string, payload Payload) error
	Close(ctx context.Context) error
}

const DeliveryKeyPrefix = "wh"

func DeliveryKey(teamID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", DeliveryKeyPrefix, teamID.String())
}
