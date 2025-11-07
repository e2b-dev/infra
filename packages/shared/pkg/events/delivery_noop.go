package events

import "context"

type NoopDelivery[Payload any] struct{}

func NewNoopDelivery[Payload any]() *NoopDelivery[Payload] {
	return &NoopDelivery[Payload]{}
}

func (n *NoopDelivery[Payload]) Publish(ctx context.Context, deliveryKey string, payload Payload) error {
	return nil
}

func (n *NoopDelivery[Payload]) Close(context.Context) error {
	return nil
}
