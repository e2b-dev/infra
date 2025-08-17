package pubsub

import "context"

// PubSub is a generic interface for a Pubsub system.
// PayloadT represents the type of the message payload to be published or consumed.
// SubMetadataT represents the type of the metadata associated with a subscription.
type PubSub[PayloadT, SubMetadataT any] interface {
	Publish(ctx context.Context, payload PayloadT) error
	Subscribe(ctx context.Context, pubSubQueue chan<- PayloadT) error
	ShouldPublish(ctx context.Context, key string) (bool, error)
	GetSubscriptionMetaData(ctx context.Context, key string) (*SubMetadataT, error)
	SetSubscriptionMetaData(ctx context.Context, key string, metaData SubMetadataT) error
	Close() error
}
