package pubsub

import "context"

type PubSub[T, M any] interface {
	Publish(ctx context.Context, payload T) error
	Subscribe(ctx context.Context, pubSubQueue chan<- T) error
	ShouldPublish(ctx context.Context, key string) (bool, error)
	GetSubscriptionMetaData(ctx context.Context, key string) (*M, error)
	SetSubscriptionMetaData(ctx context.Context, key string, metaData M) error
	Close() error
}
