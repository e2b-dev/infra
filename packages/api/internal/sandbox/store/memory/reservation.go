package memory

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Reservation struct {
	sandboxID string
	team      uuid.UUID
}

type ReservationCache struct {
	reservations *smap.Map[*Reservation]
}

func NewReservationCache() *ReservationCache {
	return &ReservationCache{
		reservations: smap.New[*Reservation](),
	}
}

func (r *ReservationCache) insertIfAbsent(sandboxID string, team uuid.UUID) bool {
	return r.reservations.InsertIfAbsent(sandboxID, &Reservation{
		team:      team,
		sandboxID: sandboxID,
	})
}

func (r *ReservationCache) release(sandboxID string) {
	r.reservations.Remove(sandboxID)
}

func (r *ReservationCache) list(teamID uuid.UUID) (sandboxIDs []string) {
	for _, item := range r.reservations.Items() {
		currentTeamID := item.team

		if currentTeamID == teamID {
			sandboxIDs = append(sandboxIDs, item.sandboxID)
		}
	}

	return sandboxIDs
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

func (s *Store) Reserve(sandboxID string, team uuid.UUID, limit int64) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Count unique IDs for team
	ids := map[string]struct{}{}

	// Get all sandbox ids (both running and those currently creating) for the team
	for _, item := range append(s.reservations.list(team), s.list(team)...) {
		ids[item] = struct{}{}
	}

	if int64(len(ids)) >= limit {
		return nil, &sandbox.LimitExceededError{TeamID: team.String()}
	}

	if _, ok := ids[sandboxID]; ok {
		return nil, &sandbox.AlreadyBeingStartedError{
			SandboxID: sandboxID,
		}
	}

	inserted := s.reservations.insertIfAbsent(sandboxID, team)
	if !inserted {
		// This shouldn't happen
		return nil, &sandbox.AlreadyBeingStartedError{
			SandboxID: sandboxID,
		}
	}

	return func() {
		// We will call this method with defer to ensure the reservation is released even if the function panics/returns an error.
		s.reservations.release(sandboxID)
	}, nil
}
