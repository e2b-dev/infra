package reservations

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// sandboxReservation tracks an in-flight sandbox creation. primarySpan holds
// the SpanContext of the caller that won the reservation race so concurrent
// waiters can attach a span link back to the main creation trace.
type sandboxReservation struct {
	start       *utils.SetOnce[sandbox.Sandbox]
	primarySpan trace.SpanContext
}

type TeamSandboxes map[string]*sandboxReservation

type ReservationStorage struct {
	reservations *smap.Map[TeamSandboxes]
}

var _ sandbox.ReservationStorage = &ReservationStorage{}

func NewReservationStorage() *ReservationStorage {
	return &ReservationStorage{
		reservations: smap.New[TeamSandboxes](),
	}
}

func (s *ReservationStorage) Reserve(ctx context.Context, teamID uuid.UUID, sandboxID string, limit int) (finishStart func(sandbox.Sandbox, error), waitForStart func(ctx context.Context) (sandbox.Sandbox, error), err error) {
	alreadyPresent := false
	limitExceeded := false
	var startResult *utils.SetOnce[sandbox.Sandbox]
	var primarySpan trace.SpanContext

	teamIDStr := teamID.String()
	s.reservations.Upsert(teamIDStr, nil, func(exist bool, teamSandboxes, _ TeamSandboxes) TeamSandboxes {
		if !exist {
			teamSandboxes = make(map[string]*sandboxReservation)
		}

		if sbx, ok := teamSandboxes[sandboxID]; ok {
			alreadyPresent = true
			startResult = sbx.start
			primarySpan = sbx.primarySpan

			return teamSandboxes
		}

		if limit >= 0 && len(teamSandboxes) >= limit {
			limitExceeded = true

			return teamSandboxes
		}

		startResult = utils.NewSetOnce[sandbox.Sandbox]()

		// Snapshot the primary caller's SpanContext once, under the map lock.
		// Zero value when the caller has no span — waiters then no-op.
		teamSandboxes[sandboxID] = &sandboxReservation{
			start:       startResult,
			primarySpan: trace.SpanContextFromContext(ctx),
		}

		return teamSandboxes
	})

	if limitExceeded {
		return nil, nil, &sandbox.LimitExceededError{TeamID: teamID}
	}

	if alreadyPresent {
		// Waiter path: link the caller's span (on the ctx passed to the
		// waitForStart closure, not the Reserve ctx) to the primary's span
		// before blocking.
		return nil, func(wctx context.Context) (sandbox.Sandbox, error) {
			telemetry.LinkSpans(wctx, primarySpan)

			return startResult.WaitWithContext(wctx)
		}, nil
	}

	return func(sbx sandbox.Sandbox, err error) {
		setErr := startResult.SetResult(sbx, err)
		if setErr != nil {
			logger.L().Error(ctx, "failed to set the result of the reservation", zap.Error(setErr), logger.WithSandboxID(sandboxID))
		}

		// Remove the reservation if the sandbox creation failed
		if err != nil {
			_ = s.Release(ctx, teamID, sandboxID)
		}
	}, nil, nil
}

func (s *ReservationStorage) Release(_ context.Context, teamID uuid.UUID, sandboxID string) error {
	teamIDStr := teamID.String()
	s.reservations.RemoveCb(teamIDStr, func(_ string, ts TeamSandboxes, exists bool) bool {
		if !exists {
			return true
		}

		delete(ts, sandboxID)

		return len(ts) == 0
	})

	return nil
}
