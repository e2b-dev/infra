package redis

import (
	"context"
)

// Notifier is the public seam onto the shared storage pub/sub infrastructure.
//
// It exposes both the consumer side (Subscribe — register a wakeup channel
// for a routing key) and the producer side (Publish — enqueue a routing key
// onto the shared publisher worker pool). Reservations and any future
// cross-package consumer depend on this rather than on subscriptionManager
// or publisher directly.
//
// Lifecycle is owned by Storage: callers MUST construct Notifier via
// Storage.Notifier(), and the underlying Storage must be Start()-ed
// before any Subscribe or Publish call. Close() on Storage shuts both
// sides down.
type Notifier struct {
	sub *subscriptionManager
	pub *publisher
}

// Subscribe registers interest in routingKey. The returned channel is
// signaled (non-blocking, drop-on-full) whenever a matching message
// arrives on the shared notify channel. The caller MUST invoke cleanup
// when done to avoid a memory leak.
func (n *Notifier) Subscribe(routingKey string) (<-chan struct{}, func()) {
	return n.sub.subscribe(routingKey)
}

// Publish enqueues routingKey for asynchronous PUBLISH on the shared
// notify channel. Never blocks: drops silently (with rate-limited warn)
// when the publish queue is saturated. Drop tolerance is part of the
// contract — every consumer ships with a fallback ticker.
func (n *Notifier) Publish(ctx context.Context, routingKey string) {
	n.pub.Publish(ctx, routingKey)
}

// Notifier returns the cross-package pub/sub seam. The returned value is
// cheap; callers may cache or re-fetch as convenient. Safe to call before
// Storage.Start, but Subscribe/Publish only function once Start is running.
func (s *Storage) Notifier() *Notifier {
	return &Notifier{sub: s.subManager, pub: s.publisher}
}
