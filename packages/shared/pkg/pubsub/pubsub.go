package pubsub

import "context"

type PubSub[T any] interface {
	Publish(ctx context.Context, payload T) error
	Subscribe(ctx context.Context, pubSubQueue chan<- T) error
	Close() error
}
