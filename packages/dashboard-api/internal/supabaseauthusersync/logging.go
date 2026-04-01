package supabaseauthusersync

import (
	"sort"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type processOutcome string

const (
	processOutcomeReadyToAck       processOutcome = "ready_to_ack"
	processOutcomeAcked            processOutcome = "acked"
	processOutcomeAckFailed        processOutcome = "ack_failed"
	processOutcomeRetried          processOutcome = "retried"
	processOutcomeRetryFailed      processOutcome = "retry_failed"
	processOutcomeDeadLettered     processOutcome = "dead_lettered"
	processOutcomeDeadLetterFailed processOutcome = "dead_letter_failed"
)

type reconcileAction string

const (
	reconcileActionUpsertPublicUser reconcileAction = "upsert_public_user"
	reconcileActionDeletePublicUser reconcileAction = "delete_public_user"
)

type processResult struct {
	Outcome  processOutcome
	Action   reconcileAction
	Duration time.Duration
	Backoff  time.Duration
}

type batchSummary struct {
	ClaimedCount          int
	AckedCount            int
	AckFailedCount        int
	RetriedCount          int
	RetryFailedCount      int
	DeadLetteredCount     int
	DeadLetterFailedCount int
	MaxAttemptCount       int32
	OldestCreatedAt       time.Time
	NewestCreatedAt       time.Time
	OldestItemAge         time.Duration
	NewestItemAge         time.Duration
	OperationCounts       map[string]int
	ActionCounts          map[string]int
}

func newBatchSummary(items []QueueItem, now time.Time) batchSummary {
	summary := batchSummary{
		ClaimedCount:    len(items),
		OperationCounts: make(map[string]int),
		ActionCounts:    make(map[string]int),
	}

	for i, item := range items {
		if i == 0 || item.AttemptCount > summary.MaxAttemptCount {
			summary.MaxAttemptCount = item.AttemptCount
		}

		summary.OperationCounts[item.Operation]++

		if item.CreatedAt.IsZero() {
			continue
		}

		if summary.OldestCreatedAt.IsZero() || item.CreatedAt.Before(summary.OldestCreatedAt) {
			summary.OldestCreatedAt = item.CreatedAt
		}
		if summary.NewestCreatedAt.IsZero() || item.CreatedAt.After(summary.NewestCreatedAt) {
			summary.NewestCreatedAt = item.CreatedAt
		}
	}

	if !summary.OldestCreatedAt.IsZero() {
		summary.OldestItemAge = ageSince(summary.OldestCreatedAt, now)
		summary.NewestItemAge = ageSince(summary.NewestCreatedAt, now)
	}

	return summary
}

func (s *batchSummary) Add(result processResult) {
	switch result.Outcome {
	case processOutcomeAcked:
		s.AckedCount++
	case processOutcomeAckFailed:
		s.AckFailedCount++
	case processOutcomeRetried:
		s.RetriedCount++
	case processOutcomeRetryFailed:
		s.RetryFailedCount++
	case processOutcomeDeadLettered:
		s.DeadLetteredCount++
	case processOutcomeDeadLetterFailed:
		s.DeadLetterFailedCount++
	}

	if result.Action != "" {
		s.ActionCounts[string(result.Action)]++
	}
}

func (s *batchSummary) Fields(totalDuration time.Duration) []zap.Field {
	fields := []zap.Field{
		zap.Int("queue_batch.claimed_count", s.ClaimedCount),
		zap.Int("queue_batch.acked_count", s.AckedCount),
		zap.Int("queue_batch.ack_failed_count", s.AckFailedCount),
		zap.Int("queue_batch.retried_count", s.RetriedCount),
		zap.Int("queue_batch.retry_failed_count", s.RetryFailedCount),
		zap.Int("queue_batch.dead_lettered_count", s.DeadLetteredCount),
		zap.Int("queue_batch.dead_letter_failed_count", s.DeadLetterFailedCount),
		zap.Int32("queue_batch.max_attempt", s.MaxAttemptCount),
		zap.Duration("queue_batch.duration", totalDuration),
	}

	if !s.OldestCreatedAt.IsZero() {
		fields = append(fields,
			logger.Time("queue_batch.oldest_item_created_at", s.OldestCreatedAt),
			logger.Time("queue_batch.newest_item_created_at", s.NewestCreatedAt),
			zap.Duration("queue_batch.oldest_item_age", s.OldestItemAge),
			zap.Duration("queue_batch.newest_item_age", s.NewestItemAge),
		)
	}

	if len(s.OperationCounts) > 0 {
		fields = append(fields, zap.Object("queue_batch.operation_counts", countsField(s.OperationCounts)))
	}
	if len(s.ActionCounts) > 0 {
		fields = append(fields, zap.Object("queue_batch.action_counts", countsField(s.ActionCounts)))
	}

	return fields
}

func (s *batchSummary) Level() zapcore.Level {
	if s.AckFailedCount > 0 || s.RetryFailedCount > 0 || s.DeadLetteredCount > 0 || s.DeadLetterFailedCount > 0 {
		return zap.ErrorLevel
	}
	if s.RetriedCount > 0 {
		return zap.WarnLevel
	}

	return zap.InfoLevel
}

func processResultFields(item QueueItem, result processResult, now time.Time) []zap.Field {
	fields := queueItemFields(item, now)
	fields = append(fields,
		zap.String("queue_item.outcome", string(result.Outcome)),
		zap.Duration("queue_item.duration", result.Duration),
	)

	if result.Action != "" {
		fields = append(fields, zap.String("queue_item.action", string(result.Action)))
	}
	if result.Backoff > 0 {
		fields = append(fields,
			zap.Duration("queue_item.retry_backoff", result.Backoff),
			zap.Int32("queue_item.next_attempt", item.AttemptCount+1),
		)
	}

	return fields
}

func queueItemFields(item QueueItem, now time.Time) []zap.Field {
	fields := []zap.Field{
		zap.Int64("queue_item.id", item.ID),
		logger.WithUserID(item.UserID.String()),
		zap.String("queue_item.operation", item.Operation),
		zap.Int32("queue_item.attempt", item.AttemptCount),
	}

	if !item.CreatedAt.IsZero() {
		fields = append(fields,
			logger.Time("queue_item.created_at", item.CreatedAt),
			zap.Duration("queue_item.age", ageSince(item.CreatedAt, now)),
		)
	}

	return fields
}

func ageSince(createdAt time.Time, now time.Time) time.Duration {
	if createdAt.IsZero() || now.Before(createdAt) {
		return 0
	}

	return now.Sub(createdAt)
}

type countsField map[string]int

func (f countsField) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		enc.AddInt(key, f[key])
	}

	return nil
}
