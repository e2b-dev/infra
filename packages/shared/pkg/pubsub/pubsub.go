package pubsub

import "context"

// PubSub is a generic interface for a Pubsub system.
// PayloadT represents the type of the message payload to be published or consumed.
// SubMetaDataT represents the type of the metadata associated with a subscription.
type PubSub[PayloadT, SubMetaDataT any] interface {
	Publish(ctx context.Context, payload PayloadT) error
	Subscribe(ctx context.Context, pubSubQueue chan<- PayloadT) error
	ShouldPublish(ctx context.Context, key string) (bool, error)
	GetSubMetaData(ctx context.Context, key string) (SubMetaDataT, error)
	SetSubMetaData(ctx context.Context, key string, metaData SubMetaDataT) error
	DeleteSubMetaData(ctx context.Context, key string) error
	Close() error
}
