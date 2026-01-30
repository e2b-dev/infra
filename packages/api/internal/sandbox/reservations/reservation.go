package reservations

import (
	"context"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type sandboxReservation struct {
	start *utils.SetOnce[sandbox.Sandbox]
}

func newSandboxReservation(start *utils.SetOnce[sandbox.Sandbox]) *sandboxReservation {
	return &sandboxReservation{
		start: start,
	}
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

	teamIDStr := teamID.String()
	s.reservations.Upsert(teamIDStr, nil, func(exist bool, teamSandboxes, _ TeamSandboxes) TeamSandboxes {
		if !exist {
			teamSandboxes = make(map[string]*sandboxReservation)
		}

		if sbx, ok := teamSandboxes[sandboxID]; ok {
			alreadyPresent = true
			startResult = sbx.start

			return teamSandboxes
		}

		if limit >= 0 && len(teamSandboxes) >= limit {
			limitExceeded = true

			return teamSandboxes
		}

		startResult = utils.NewSetOnce[sandbox.Sandbox]()
		teamSandboxes[sandboxID] = newSandboxReservation(startResult)

		return teamSandboxes
	})

	if limitExceeded {
		return nil, nil, &sandbox.LimitExceededError{TeamID: teamID}
	}

	if alreadyPresent {
		return nil, startResult.WaitWithContext, nil
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
