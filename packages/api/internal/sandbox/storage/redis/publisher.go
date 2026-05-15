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
	publishQueueDepth = 8192

	// publishTimeout bounds a single PUBLISH round-trip
	publishTimeout = 5 * time.Second

	// publishShutdownBudget caps the total time spent draining the queue
	// after Close. A hung Redis cannot block teardown indefinitely.
	publishShutdownBudget = 5 * time.Second

	// publishDropLogInterval rate-limits drop warnings: only every Nth
	// drop produces a log line, with the running total attached.
	publishDropLogInterval = 64

	// publishWorkerCount is the size of the goroutine pool draining the
	// publish queue. Each worker is mostly blocked in Redis RTT, so the
	// effective steady-state throughput is ~N / RTT. Sized to comfortably
	// exceed any realistic Release()/transition notification rate while
	// staying well under the Redis client's connection pool.
	publishWorkerCount = 16
)

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
	Publish(ctx context.Context, routingKey string)
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
func (p *publisher) Publish(ctx context.Context, routingKey string) {
	// Fast reject if Close has been signalled; otherwise a send into a
	// closing publisher could land in the queue after the drainer exits.
	select {
	case <-p.closed:
		p.drop(ctx, routingKey)

		return
	default:
	}

	select {
	case p.queue <- routingKey:
	default:
		p.drop(ctx, routingKey)
	}
}

func (p *publisher) drop(ctx context.Context, routingKey string) {
	n := p.dropped.Add(1)
	if n%publishDropLogInterval == 1 {
		logger.L().Warn(ctx,
			"Dropping storage notification: publish queue saturated or closed",
			zap.String("routing_key", routingKey),
			zap.Uint64("total_drops", n),
		)
	}
}

// run starts the worker pool and blocks until the pool exits.
// On exit it performs a bounded best-effort drain of pending items so a
// graceful shutdown does not lose every in-flight notification.
func (p *publisher) run(ctx context.Context) {
	p.started.Store(true)
	defer close(p.done)

	pubCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Bridge close() into pubCtx so in-flight publishOne calls abort when
	// the publisher is closed, not just when the parent is cancelled.
	go func() {
		select {
		case <-p.closed:
			cancel()
		case <-pubCtx.Done():
		}
	}()

	var wg sync.WaitGroup
	for range publishWorkerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.workerLoop(pubCtx)
		}()
	}
	wg.Wait()

	p.drainOnShutdown(ctx)
}

// workerLoop is the per-worker drain loop. Multiple workers contend on
// the same queue; Go channel receives are serialized internally, so each
// key is delivered to exactly one worker.
func (p *publisher) workerLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case key := <-p.queue:
			p.publishOne(ctx, key)
		}
	}
}

// drainOnShutdown opportunistically publishes any keys still in the queue,
// using a single shared deadline so a hung Redis cannot block teardown.
func (p *publisher) drainOnShutdown(ctx context.Context) {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishShutdownBudget)
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
// the shutdown budget elapses). Safe to call multiple times.
func (p *publisher) close() {
	p.closeOnce.Do(func() { close(p.closed) })
	if p.started.Load() {
		<-p.done
	}
}
