package memory

import (
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type Reservation struct {
	sandboxID string
	team      uuid.UUID
	start     *utils.SetOnce[sandbox.Sandbox]
}

type ReservationCache struct {
	reservations *smap.Map[*Reservation]
}

func NewReservationCache() *ReservationCache {
	return &ReservationCache{
		reservations: smap.New[*Reservation](),
	}
}

func (r *ReservationCache) insertIfAbsent(sandboxID string, team uuid.UUID, start *utils.SetOnce[sandbox.Sandbox]) bool {
	return r.reservations.InsertIfAbsent(sandboxID, &Reservation{
		team:      team,
		sandboxID: sandboxID,
		start:     start,
	})
}

func (r *ReservationCache) release(sandboxID string) {
	r.reservations.Remove(sandboxID)
}

func (r *ReservationCache) list(teamID uuid.UUID) (reservations []*Reservation) {
	for _, item := range r.reservations.Items() {
		currentTeamID := item.team

		if currentTeamID == teamID {
			reservations = append(reservations, item)
		}
	}

	return reservations
}

func (s *Store) list(teamID uuid.UUID) (sandboxIDs []string) {
	for _, value := range s.items.Items() {
		currentTeamID := value.TeamID()

		if currentTeamID == teamID {
			sandboxIDs = append(sandboxIDs, value.SandboxID())
		}
	}

	return sandboxIDs
}

func (s *Store) Reserve(sandboxID string, team uuid.UUID, limit int64) (release func(sandbox.Sandbox, error), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Count unique IDs for team
	ids := make(map[string]*Reservation)

	// Get all sandbox ids (both running and those currently creating) for the team
	for _, item := range s.list(team) {
		ids[item] = nil
	}

	for _, item := range s.reservations.list(team) {
		ids[item.sandboxID] = item
	}

	if int64(len(ids)) >= limit {
		return nil, &sandbox.LimitExceededError{TeamID: team.String()}
	}

	if sbx, ok := ids[sandboxID]; ok {
		var start *utils.SetOnce[sandbox.Sandbox]
		if sbx != nil {
			start = sbx.start
		}

		return nil, &sandbox.AlreadyBeingStartedError{
			SandboxID: sandboxID,
			Start:     start,
		}
	}

	start := utils.NewSetOnce[sandbox.Sandbox]()
	inserted := s.reservations.insertIfAbsent(sandboxID, team, start)
	if !inserted {
		// This shouldn't happen
		return nil, &sandbox.AlreadyBeingStartedError{
			SandboxID: sandboxID,
		}
	}

	return func(sbx sandbox.Sandbox, err error) {
		setErr := start.SetResult(sbx, err)
		if setErr != nil {
			zap.L().Error("failed to set the result of the reservation", zap.Error(setErr), logger.WithSandboxID(sandboxID))
		}

		// We will call this method with defer to ensure the reservation is released even if the function panics/returns an error.
		s.reservations.release(sandboxID)
	}, nil
}
