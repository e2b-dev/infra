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
	resultTTL     = 30 * time.Second
	retryInterval = 20 * time.Millisecond

	// staleTTL is the maximum age of a pending entry before it is considered stale
	// and cleaned up. This handles the case where an API instance crashes mid-creation.
	// 90 seconds is well beyond any realistic sandbox creation time.
	staleTTL = 90 * time.Second

	// redisOpTimeout bounds a single Redis round trip. Each call site wraps its
	// context with this timeout so a stuck Redis call cannot block callers
	// indefinitely.
	redisOpTimeout = 5 * time.Second
)

var _ sandbox.ReservationStorage = (*ReservationStorage)(nil)

type ReservationStorage struct {
	redisClient redis.UniversalClient
}

func NewReservationStorage(redisClient redis.UniversalClient) *ReservationStorage {
	return &ReservationStorage{
		redisClient: redisClient,
	}
}

func (s *ReservationStorage) Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(sandbox.Sandbox, error), waitForStart func(ctx context.Context) (sandbox.Sandbox, error), err error) {
	teamIDStr := teamID.String()
	storageIndexKey := getStorageIndexKey(teamIDStr)
	pendingSetKey := getPendingSetKey(teamIDStr)
	resultKeyStr := getResultKey(teamIDStr, sandboxID)

	now := float64(time.Now().Unix())
	staleCutoff := float64(time.Now().Add(-staleTTL).Unix())

	reserveCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	result, err := reserveScript.Run(reserveCtx, s.redisClient,
		[]string{storageIndexKey, pendingSetKey, resultKeyStr},
		sandboxID, limit, now, staleCutoff,
	).Int()
	cancel()
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

	releaseCtx, cancel := context.WithTimeout(ctx, redisOpTimeout)
	err := releaseScript.Run(releaseCtx, s.redisClient, []string{pendingSetKey, resultKeyStr}, sandboxID).Err()
	cancel()
	if err != nil {
		return fmt.Errorf("failed to run release script: %w", err)
	}

	return nil
}

// createFinishStart returns a callback that completes the reservation.
// It removes the sandbox from the pending zset and stores the result for cross-instance waiters.
func (s *ReservationStorage) createFinishStart(ctx context.Context, teamID uuid.UUID, sandboxID string) func(sandbox.Sandbox, error) {
	return func(sbx sandbox.Sandbox, startErr error) {
		teamIDStr := teamID.String()
		pendingSetKey := getPendingSetKey(teamIDStr)
		resultKeyStr := getResultKey(teamIDStr, sandboxID)

		resultData, encodeErr := encodeResult(sbx, startErr)
		if encodeErr != nil {
			logger.L().Error(ctx, "failed to encode reservation result",
				zap.Error(encodeErr),
				logger.WithSandboxID(sandboxID),
			)

			// Still try to remove from pending even if encoding fails
			zremCtx, zremCancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
			_ = s.redisClient.ZRem(zremCtx, pendingSetKey, sandboxID).Err()
			zremCancel()

			return
		}

		ttlSeconds := int(resultTTL.Seconds())
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), redisOpTimeout)
		err := finishStartScript.Run(finishCtx, s.redisClient,
			[]string{pendingSetKey, resultKeyStr},
			sandboxID, resultData, ttlSeconds,
		).Err()
		finishCancel()
		if err != nil {
			logger.L().Error(ctx, "failed to run finishStart script",
				zap.Error(err),
				logger.WithSandboxID(sandboxID),
			)
		}
	}
}

// createWaitForStart returns a function that polls Redis for the result of a sandbox creation
// initiated by another instance.
func (s *ReservationStorage) createWaitForStart(teamID uuid.UUID, sandboxID string) func(ctx context.Context) (sandbox.Sandbox, error) {
	return func(ctx context.Context) (sandbox.Sandbox, error) {
		teamIDStr := teamID.String()
		resultKeyStr := getResultKey(teamIDStr, sandboxID)
		pendingSetKey := getPendingSetKey(teamIDStr)

		for {
			// Check for result
			getCtx, getCancel := context.WithTimeout(ctx, redisOpTimeout)
			data, err := s.redisClient.Get(getCtx, resultKeyStr).Bytes()
			getCancel()
			if err == nil {
				return decodeResult(data)
			}
			if !errors.Is(err, redis.Nil) {
				return sandbox.Sandbox{}, fmt.Errorf("failed to check result key: %w", err)
			}

			// No result yet — check if still pending (ZSCORE returns nil if not a member)
			zscoreCtx, zscoreCancel := context.WithTimeout(ctx, redisOpTimeout)
			err = s.redisClient.ZScore(zscoreCtx, pendingSetKey, sandboxID).Err()
			zscoreCancel()
			if errors.Is(err, redis.Nil) {
				// Not pending anymore, final check
				finalGetCtx, finalGetCancel := context.WithTimeout(ctx, redisOpTimeout)
				data, err = s.redisClient.Get(finalGetCtx, resultKeyStr).Bytes()
				finalGetCancel()
				if err == nil {
					return decodeResult(data)
				}

				return sandbox.Sandbox{}, fmt.Errorf("sandbox %s is no longer pending and has no result", sandboxID)
			}
			if err != nil {
				return sandbox.Sandbox{}, fmt.Errorf("failed to check pending set: %w", err)
			}

			// Wait before next poll
			select {
			case <-ctx.Done():
				return sandbox.Sandbox{}, ctx.Err()
			case <-time.After(retryInterval):
				// continue polling
			}
		}
	}
}
