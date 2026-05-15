package redis

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	// publishQueueDepth caps the in-flight backlog of routing keys.
	// Drops here are correctness-safe: lock waiters fall back to the
	// jittered 200ms-1s timer in storageLocker.Obtain and transition
	// waiters fall back to the 1s poll ticker in waitForTransition.
	publishQueueDepth = 4096

	// publishTimeout bounds a single PUBLISH round-trip. Mirrors the
	// previous per-goroutine timeout so behavior on a slow/dead Redis
	// is unchanged: log and move on.
	publishTimeout = 5 * time.Second

	// publishShutdownBudget caps the total time spent draining the queue
	// after Close. A hung Redis cannot block teardown indefinitely.
	publishShutdownBudget = 5 * time.Second

	// publishDropLogInterval rate-limits drop warnings: only every Nth
	// drop produces a log line, with the running total attached.
	publishDropLogInterval = 1024
)

// publisher serializes best-effort PubSub notifications onto a single
// long-lived goroutine, eliminating the per-Release goroutine spawn.
// Callers hand it a routing-key string via Publish; one drainer goroutine
// publishes them on the global notify channel.
//
// Backpressure policy: drop and count. Subscribers tolerate dropped
// notifications by design (see Obtain and waitForTransition fallback
// timers), so a bounded queue is the correct primitive — an unbounded
// one would just relocate this from a goroutine leak to a memory leak.
type publisher struct {
	redisClient redis.UniversalClient
	channel     string

	queue  chan string
	closed chan struct{} // signals the drainer to stop and reject new sends
	done   chan struct{} // closed when the drainer has fully exited

	closeOnce sync.Once
	started   atomic.Bool // set true on entry to run()
	dropped   atomic.Uint64
}

// notifier is the narrow message-only seam consumed by storageLock.
// Defining it where it is consumed (lock.go-side) lets storageLock be
// faked in tests without standing up Redis.
type notifier interface {
	Publish(routingKey string)
}

func newPublisher(redisClient redis.UniversalClient, channel string) *publisher {
	return &publisher{
		redisClient: redisClient,
		channel:     channel,
		queue:       make(chan string, publishQueueDepth),
		closed:      make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Publish enqueues a routing key for asynchronous PUBLISH. Never blocks.
// Drops silently (with rate-limited warn) when the queue is full or the
// publisher has been closed.
func (p *publisher) Publish(routingKey string) {
	// Fast reject if Close has been signalled; otherwise a send into a
	// closing publisher could land in the queue after the drainer exits.
	select {
	case <-p.closed:
		p.drop(routingKey)
		return
	default:
	}

	select {
	case p.queue <- routingKey:
	default:
		p.drop(routingKey)
	}
}

func (p *publisher) drop(routingKey string) {
	n := p.dropped.Add(1)
	if n%publishDropLogInterval == 1 {
		logger.L().Warn(context.Background(),
			"Dropping storage notification: publish queue saturated or closed",
			zap.String("routing_key", routingKey),
			zap.Uint64("total_drops", n),
		)
	}
}

// run drains the queue. Intended to run in a single goroutine for the
// lifetime of Storage. Returns when ctx is cancelled OR close() is called.
// On exit it performs a bounded best-effort drain of pending items so a
// graceful shutdown does not lose every in-flight notification.
//
// In-flight publishes inherit a derived context that is cancelled by
// either the parent ctx or close(), so a hung Redis cannot delay Close
// beyond publishShutdownBudget.
func (p *publisher) run(ctx context.Context) {
	p.started.Store(true)
	defer close(p.done)

	pubCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bridge close() into pubCtx so an in-flight publishOne aborts when
	// the publisher is closed, not just when the parent is cancelled.
	go func() {
		select {
		case <-p.closed:
			cancel()
		case <-pubCtx.Done():
		}
	}()

	for {
		select {
		case <-pubCtx.Done():
			p.drainOnShutdown()
			return
		case key := <-p.queue:
			p.publishOne(pubCtx, key)
		}
	}
}

// drainOnShutdown opportunistically publishes any keys still in the queue,
// using a single shared deadline so a hung Redis cannot block teardown.
func (p *publisher) drainOnShutdown() {
	drainCtx, cancel := context.WithTimeout(context.Background(), publishShutdownBudget)
	defer cancel()

	for {
		select {
		case key := <-p.queue:
			p.publishOne(drainCtx, key)
			if drainCtx.Err() != nil {
				return
			}
		default:
			return
		}
	}
}

func (p *publisher) publishOne(ctx context.Context, routingKey string) {
	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()

	if err := p.redisClient.Publish(pubCtx, p.channel, routingKey).Err(); err != nil {
		logger.L().Warn(pubCtx, "Failed to publish storage notification",
			zap.String("routing_key", routingKey),
			zap.Error(err),
		)
	}
}

// close signals the drainer to stop and blocks until it has exited (or
// the shutdown budget elapses). Safe to call multiple times and safe to
// call before run() has ever started (e.g. on an early-init failure path
// where Storage.Close is invoked before Storage.Start). In the
// not-started case there is no drainer to wait on, so we skip the join.
func (p *publisher) close() {
	p.closeOnce.Do(func() { close(p.closed) })
	if p.started.Load() {
		<-p.done
	}
}

// dropCount returns the total number of dropped publishes since creation.
// Intended for observability and tests; not load-bearing for correctness.
func (p *publisher) dropCount() uint64 {
	return p.dropped.Load()
}
