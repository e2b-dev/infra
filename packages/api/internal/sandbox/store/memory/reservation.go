package memory

import (
	"context"

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

func NewReservationStorage() *ReservationStorage {
	return &ReservationStorage{
		reservations: smap.New[TeamSandboxes](),
	}
}

func (s *ReservationStorage) Reserve(teamID, sandboxID string, limit int64) (finishStart func(sandbox.Sandbox, error), waitForStart func(ctx context.Context) (sandbox.Sandbox, error), err error) {
	alreadyPresent := false
	limitExceeded := false
	var startResult *utils.SetOnce[sandbox.Sandbox]

	s.reservations.Upsert(teamID, nil, func(exist bool, teamSandboxes, _ TeamSandboxes) TeamSandboxes {
		if !exist {
			teamSandboxes = make(map[string]*sandboxReservation)
		}

		if sbx, ok := teamSandboxes[sandboxID]; ok {
			alreadyPresent = true
			startResult = sbx.start
			return teamSandboxes
		}

		if limit >= 0 && len(teamSandboxes) >= int(limit) {
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
			zap.L().Error("failed to set the result of the reservation", zap.Error(setErr), logger.WithSandboxID(sandboxID))
		}

		// Remove the reservation if the sandbox creation failed
		if err != nil {
			s.Remove(teamID, sandboxID)
		}
	}, nil, nil
}

func (s *ReservationStorage) Remove(teamID, sandboxID string) {
	s.reservations.RemoveCb(teamID, func(_ string, ts TeamSandboxes, exists bool) bool {
		if !exists {
			return true
		}

		delete(ts, sandboxID)
		return len(ts) == 0
	})
}
