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

type TeamSandboxes struct {
	teamID    string
	sandboxes map[string]*sandboxReservation
}

type ReservationStorage struct {
	reservations *smap.Map[*TeamSandboxes]
}

func NewReservationStorage() *ReservationStorage {
	return &ReservationStorage{
		reservations: smap.New[*TeamSandboxes](),
	}
}

func (s *ReservationStorage) Reserve(teamID, sandboxID string, limit int64) (finishStart func(sandbox.Sandbox, error), waitForStart func(ctx context.Context) (sandbox.Sandbox, error), err error) {
	alreadyPresent := false
	startResult := utils.NewSetOnce[sandbox.Sandbox]()
	limitExceeded := false

	s.reservations.Upsert(teamID, &TeamSandboxes{
		teamID:    teamID,
		sandboxes: make(map[string]*sandboxReservation),
	}, func(exist bool, valueInMap, newValue *TeamSandboxes) *TeamSandboxes {
		if exist {
			if sbx, ok := valueInMap.sandboxes[sandboxID]; ok {
				alreadyPresent = true
				startResult = sbx.start
				return valueInMap
			}

			if limit > 0 && len(valueInMap.sandboxes) >= int(limit) {
				limitExceeded = true
				return valueInMap
			}

			valueInMap.sandboxes[sandboxID] = &sandboxReservation{start: startResult}
			return valueInMap
		}

		newValue.sandboxes[sandboxID] = &sandboxReservation{start: startResult}
		return newValue
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

		if err != nil {
			s.Remove(teamID, sandboxID)
		}
	}, nil, nil
}

func (s *ReservationStorage) Remove(teamID, sandboxID string) {
	s.reservations.RemoveCb(teamID, func(key string, ts *TeamSandboxes, exists bool) bool {
		if !exists {
			return true
		}

		delete(ts.sandboxes, sandboxID)
		return len(ts.sandboxes) == 0
	})
}
