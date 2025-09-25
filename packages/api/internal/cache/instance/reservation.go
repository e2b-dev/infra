package instance

import (
	"fmt"

	"github.com/google/uuid"

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

func (ms *MemoryStore) list(teamID uuid.UUID) (sandboxIDs []string) {
	for _, value := range ms.items.Items() {
		currentTeamID := value.TeamID()

		if currentTeamID == teamID {
			sandboxIDs = append(sandboxIDs, value.SandboxID())
		}
	}

	return sandboxIDs
}

type AlreadyBeingStartedError struct {
	sandboxID string
}

func (e *AlreadyBeingStartedError) Error() string {
	return fmt.Sprintf("sandbox %s is already being started", e.sandboxID)
}

type SandboxLimitExceededError struct {
	teamID string
}

func (e *SandboxLimitExceededError) Error() string {
	return fmt.Sprintf("sandbox %s has exceeded the limit", e.teamID)
}

func (ms *MemoryStore) Reserve(sandboxID string, team uuid.UUID, limit int64) (release func(), err error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	// Count unique IDs for team
	ids := map[string]struct{}{}

	// Get all sandbox ids (both running and those currently creating) for the team
	for _, item := range append(ms.reservations.list(team), ms.list(team)...) {
		ids[item] = struct{}{}
	}

	if int64(len(ids)) >= limit {
		return nil, &SandboxLimitExceededError{teamID: team.String()}
	}

	if _, ok := ids[sandboxID]; ok {
		return nil, &AlreadyBeingStartedError{
			sandboxID: sandboxID,
		}
	}

	inserted := ms.reservations.insertIfAbsent(sandboxID, team)
	if !inserted {
		// This shouldn't happen
		return nil, &AlreadyBeingStartedError{
			sandboxID: sandboxID,
		}
	}

	return func() {
		// We will call this method with defer to ensure the reservation is released even if the function panics/returns an error.
		ms.reservations.release(sandboxID)
	}, nil
}
