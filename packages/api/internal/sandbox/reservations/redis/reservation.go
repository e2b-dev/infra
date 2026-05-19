package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	resultTTL = 30 * time.Second

	// fallbackPollInterval is how often the waiter re-checks Redis when no
	// PubSub wakeup arrives. PubSub is the primary wakeup mechanism; this
	// ticker is the safety net for dropped messages (network blips, queue
	// saturation on the publisher worker pool, Redis reconnects).
	//
	// Matches storage/redis pollInterval so reservations and state-change
	// share a single tail-latency story for missed notifications.
	fallbackPollInterval = 1 * time.Second

	// staleTTL is the maximum age of a pending entry before it is considered stale
	// and cleaned up. This handles the case where an API instance crashes mid-creation.
	// 90 seconds is well beyond any realistic sandbox creation time.
	staleTTL = 90 * time.Second
)

var _ sandbox.ReservationStorage = (*ReservationStorage)(nil)

// Notifier is the consumer-side view of the shared storage pub/sub seam.
// It is satisfied structurally by *storage_redis.Notifier, but accepting
// the interface keeps this package free of an explicit storage dependency
// and lets tests inject a fake. Publish is fire-and-forget — drops on
// queue saturation are recovered by the waiter's fallback ticker.
type Notifier interface {
	Subscribe(routingKey string) (<-chan struct{}, func())
	Publish(ctx context.Context, routingKey string)
}

type ReservationStorage struct {
	redisClient redis.UniversalClient
	notifier    Notifier
}

func NewReservationStorage(redisClient redis.UniversalClient, notifier Notifier) *ReservationStorage {
	return &ReservationStorage{
		redisClient: redisClient,
		notifier:    notifier,
	}
}

func (s *ReservationStorage) Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(sandbox.Sandbox, error), waitForStart func(ctx context.Context) (sandbox.Sandbox, error), err error) {
	teamIDStr := teamID.String()
	storageIndexKey := getStorageIndexKey(teamIDStr)
	pendingSetKey := getPendingSetKey(teamIDStr)
	resultKeyStr := getResultKey(teamIDStr, sandboxID)

	now := float64(time.Now().Unix())
	staleCutoff := float64(time.Now().Add(-staleTTL).Unix())

	result, err := reserveScript.Run(ctx, s.redisClient,
		[]string{storageIndexKey, pendingSetKey, resultKeyStr},
		sandboxID, limit, now, staleCutoff,
	).Int()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to run reserve script: %w", err)
	}

	switch result {
	case reserveResultReserved:
		return s.createFinishStart(ctx, teamID, sandboxID), nil, nil

	case reserveResultAlreadyInStorage:
		return nil, nil, sandbox.ErrAlreadyExists

	case reserveResultAlreadyPending:
		return nil, s.createWaitForStart(teamID, sandboxID), nil

	case reserveResultLimitExceeded:
		return nil, nil, &sandbox.LimitExceededError{TeamID: teamID}

	default:
		return nil, nil, fmt.Errorf("unexpected reserve script result: %d", result)
	}
}

func (s *ReservationStorage) Release(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	teamIDStr := teamID.String()
	pendingSetKey := getPendingSetKey(teamIDStr)
	resultKeyStr := getResultKey(teamIDStr, sandboxID)

	tombstone, err := encodeReleased()
	if err != nil {
		return fmt.Errorf("failed to encode release tombstone: %w", err)
	}
	ttlSeconds := int(resultTTL.Seconds())

	err = releaseScript.Run(ctx, s.redisClient,
		[]string{pendingSetKey, resultKeyStr},
		sandboxID, tombstone, ttlSeconds,
	).Err()
	if err != nil {
		return fmt.Errorf("failed to run release script: %w", err)
	}

	// Wake any in-process waiter so it reads the tombstone immediately
	// rather than after the fallback ticker. Drop-tolerant.
	s.notifier.Publish(ctx, getReservationRoutingKey(teamIDStr, sandboxID))

	return nil
}

// createFinishStart returns a callback that completes the reservation.
// It removes the sandbox from the pending zset and stores the result for
// cross-instance waiters, then publishes a wakeup on the shared notify
// channel so subscribed waiters resolve without waiting for the fallback
// ticker.
func (s *ReservationStorage) createFinishStart(ctx context.Context, teamID uuid.UUID, sandboxID string) func(sandbox.Sandbox, error) {
	return func(sbx sandbox.Sandbox, startErr error) {
		teamIDStr := teamID.String()
		pendingSetKey := getPendingSetKey(teamIDStr)
		resultKeyStr := getResultKey(teamIDStr, sandboxID)
		routingKey := getReservationRoutingKey(teamIDStr, sandboxID)

		bgCtx := context.WithoutCancel(ctx)

		resultData, encodeErr := encodeResult(sbx, startErr)
		if encodeErr != nil {
			logger.L().Error(ctx, "failed to encode reservation result",
				zap.Error(encodeErr),
				logger.WithSandboxID(sandboxID),
			)

			// Best-effort: write a released tombstone so waiters resolve
			// with a typed error rather than blocking until the result
			// key TTL elapses. Falls back to a plain ZRem if even encoding
			// the tombstone fails.
			tombstone, tsErr := encodeReleased()
			if tsErr != nil {
				_ = s.redisClient.ZRem(bgCtx, pendingSetKey, sandboxID).Err()
			} else {
				_ = releaseScript.Run(bgCtx, s.redisClient,
					[]string{pendingSetKey, resultKeyStr},
					sandboxID, tombstone, int(resultTTL.Seconds()),
				).Err()
			}
			// Still wake waiters so they read the tombstone (or fall back
			// to the ticker if even ZRem failed).
			s.notifier.Publish(bgCtx, routingKey)

			return
		}

		ttlSeconds := int(resultTTL.Seconds())
		err := finishStartScript.Run(bgCtx, s.redisClient,
			[]string{pendingSetKey, resultKeyStr},
			sandboxID, resultData, ttlSeconds,
		).Err()
		if err != nil {
			logger.L().Error(ctx, "failed to run finishStart script",
				zap.Error(err),
				logger.WithSandboxID(sandboxID),
			)

			return
		}

		// Wake any in-process waiter immediately. Drop-tolerant: the
		// fallback ticker covers a saturated publish queue.
		s.notifier.Publish(bgCtx, routingKey)
	}
}

// createWaitForStart returns a function that polls Redis for the result of a sandbox creation
// initiated by another instance.
func (s *ReservationStorage) createWaitForStart(teamID uuid.UUID, sandboxID string) func(ctx context.Context) (sandbox.Sandbox, error) {
	return func(ctx context.Context) (sandbox.Sandbox, error) {
		return s.waitForResult(ctx, teamID, sandboxID)
	}
}

// waitForResult blocks until the reservation initiated by another instance
// either completes (result key set with a sandbox or producer error) or is
// released (result key set with a tombstone resolving to
// sandbox.ErrReservationReleased).
//
// PubSub is the primary wakeup channel: createFinishStart and Release both
// publish on the shared notify channel after their atomic Redis writes
// complete. A 1s fallback ticker recovers any dropped notification — by
// design, the publisher worker pool can drop on saturation and the waiter
// must tolerate that.
//
// Ordering: we subscribe BEFORE the initial GET so a publish landing in the
// window between (Reserve returning AlreadyPending) and (the waiter calling
// Subscribe) cannot be missed.
func (s *ReservationStorage) waitForResult(ctx context.Context, teamID uuid.UUID, sandboxID string) (sandbox.Sandbox, error) {
	teamIDStr := teamID.String()
	resultKeyStr := getResultKey(teamIDStr, sandboxID)
	pendingSetKey := getPendingSetKey(teamIDStr)
	routingKey := getReservationRoutingKey(teamIDStr, sandboxID)

	ch, cleanup := s.notifier.Subscribe(routingKey)
	defer cleanup()

	// Initial probe: the producer may have finished before we subscribed,
	// or we may be a late waiter joining after the result was already set.
	if done, sbx, err := s.tryReadResult(ctx, resultKeyStr, pendingSetKey, sandboxID); done {
		return sbx, err
	}

	ticker := time.NewTicker(fallbackPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return sandbox.Sandbox{}, ctx.Err()
		case <-ch:
		case <-ticker.C:
		}

		if done, sbx, err := s.tryReadResult(ctx, resultKeyStr, pendingSetKey, sandboxID); done {
			return sbx, err
		}
	}
}

// tryReadResult performs a single probe of the reservation state.
//
// With the tombstone-on-Release contract, the result key is the single
// source of truth: any terminal state (success, producer error, or
// release) is encoded there. We only consult the pending zset as a
// safety net for legacy entries written by older instances that did not
// tombstone — those entries decay via stale-GC, so this branch will be
// dead in steady state after deploy.
//
// Returns done=true when the wait is over:
//   - the result key holds an encoded terminal result, or
//   - the sandbox vanished from the pending set without a tombstone
//     (legacy compatibility path), or
//   - the Redis call itself failed.
//
// Returns done=false when the reservation is still pending and the caller
// should wait for the next wakeup.
func (s *ReservationStorage) tryReadResult(
	ctx context.Context,
	resultKey, pendingSetKey, sandboxID string,
) (done bool, sbx sandbox.Sandbox, err error) {
	data, getErr := s.redisClient.Get(ctx, resultKey).Bytes()
	if getErr == nil {
		sbx, err = decodeResult(data)

		return true, sbx, err
	}
	if !errors.Is(getErr, redis.Nil) {
		return true, sandbox.Sandbox{}, fmt.Errorf("failed to check result key: %w", getErr)
	}

	// No result yet. Confirm the reservation is still pending; if it's
	// not, this is a pre-tombstone Release from a legacy instance. Treat
	// it as a release for compatibility.
	//
	// TODO [ENG-4089]: drop this ZSCORE fallback once all
	// instances are guaranteed to write a tombstone on Release.
	scoreErr := s.redisClient.ZScore(ctx, pendingSetKey, sandboxID).Err()
	if errors.Is(scoreErr, redis.Nil) {
		// Final read in case finishStart/release raced between our GET
		// and ZSCORE.
		data, getErr = s.redisClient.Get(ctx, resultKey).Bytes()
		if getErr == nil {
			sbx, err = decodeResult(data)

			return true, sbx, err
		}

		return true, sandbox.Sandbox{}, sandbox.ErrReservationReleased
	}
	if scoreErr != nil {
		return true, sandbox.Sandbox{}, fmt.Errorf("failed to check pending set: %w", scoreErr)
	}

	// Still pending, no result yet.
	return false, sandbox.Sandbox{}, nil
}
