package instance

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Reservation struct {
	instanceID string
	team       uuid.UUID
}

type ReservationCache struct {
	reservations *smap.Map[*Reservation]
}

func NewReservationCache() *ReservationCache {
	return &ReservationCache{
		reservations: smap.New[*Reservation](),
	}
}

func (r *ReservationCache) insertIfAbsent(instanceID string, team uuid.UUID) bool {
	return r.reservations.InsertIfAbsent(instanceID, &Reservation{
		team:       team,
		instanceID: instanceID,
	})
}

func (r *ReservationCache) release(instanceID string) {
	r.reservations.Remove(instanceID)
}

func (r *ReservationCache) list(teamID uuid.UUID) (instanceIDs []string) {
	for _, item := range r.reservations.Items() {
		currentTeamID := item.team

		if currentTeamID == teamID {
			instanceIDs = append(instanceIDs, item.instanceID)
		}
	}

	return instanceIDs
}

func (c *InstanceCache) list(teamID uuid.UUID) (instanceIDs []string) {
	for _, value := range c.cache.Items() {
		currentTeamID := value.TeamID

		if currentTeamID == nil {
			continue
		}

		if *currentTeamID == teamID {
			instanceIDs = append(instanceIDs, value.Instance.SandboxID)
		}
	}

	return instanceIDs
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

func (c *InstanceCache) Reserve(instanceID string, team uuid.UUID, limit int64) (release func(), err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Count unique IDs for team
	ids := map[string]struct{}{}

	// Get all sandbox ids (both running and those currently creating) for the team
	for _, item := range append(c.reservations.list(team), c.list(team)...) {
		ids[item] = struct{}{}
	}

	if int64(len(ids)) >= limit {
		return nil, &SandboxLimitExceededError{teamID: team.String()}
	}

	if _, ok := ids[instanceID]; ok {
		return nil, &AlreadyBeingStartedError{
			sandboxID: instanceID,
		}
	}

	inserted := c.reservations.insertIfAbsent(instanceID, team)
	if !inserted {
		// This shouldn't happen
		return nil, &AlreadyBeingStartedError{
			sandboxID: instanceID,
		}
	}

	return func() {
		// We will call this method with defer to ensure the reservation is released even if the function panics/returns an error.
		c.reservations.release(instanceID)
	}, nil
}
