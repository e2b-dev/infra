// Package management holds the state changes the control-plane management
// interface applies. They live outside the handlers so they are reachable
// without gin: what these operations get wrong is never the HTTP.
package management

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// ErrProjectNotFound reports a project unknown to this cluster. Returned rather
// than answered here, because each route reads it differently.
var ErrProjectNotFound = errors.New("project not found")

// Service applies membership changes and evicts the cache entries each one
// invalidates.
//
// The two halves are inseparable. Auth caches a copy of the team per member
// under <userID>-<teamID>, and InvalidateTeamCache finds those keys by reading
// users_teams — so a member already removed is one it cannot see. Writing and
// evicting from separate call sites leaves a revoked member authenticating
// until the entry expires, which is why there is no way to do only one.
type Service struct {
	db    *authdb.Client
	cache sharedauth.Service
}

func NewService(db *authdb.Client, cache sharedauth.Service) *Service {
	return &Service{db: db, cache: cache}
}

// MemberChange is the membership a push states for a project. Users it does not
// name keep whatever membership they have.
type MemberChange struct {
	ProjectID uuid.UUID
	Present   []uuid.UUID
	Absent    []uuid.UUID

	// AddedBy records the actor behind the addition, when the caller names one.
	AddedBy *uuid.UUID
}

// namedUsers is every user the change states a presence for.
func (c MemberChange) namedUsers() []uuid.UUID {
	return slices.Concat(c.Present, c.Absent)
}

// anchoredUsers lists the user rows adding Present depends on: the members, and
// whoever is recorded as adding them.
func (c MemberChange) anchoredUsers() []uuid.UUID {
	if c.AddedBy == nil {
		return c.Present
	}

	return append(slices.Clone(c.Present), *c.AddedBy)
}

// SetProjectMembers reconciles a stated membership against a project in one
// transaction.
//
// Idempotent in both directions, because the caller retries, delivers at least
// once and lets its own pushes interleave.
//
// The rules the dashboard's member routes enforce — no removing the last
// member, no touching a default membership — are deliberately absent. The
// caller owns membership, and a rule here would make its pushes unrepeatable.
func (s *Service) SetProjectMembers(ctx context.Context, change MemberChange) error {
	removed, err := s.applyMembers(ctx, change)
	if err != nil {
		return err
	}

	// Evicting before the commit would let a concurrent read repopulate the
	// entry with uncommitted state.
	//
	// Every user the change named, not only the rows that moved: a crash here
	// leaves stale entries that only the caller's retry clears, and that retry
	// finds the work already done. Repeating an eviction costs a cache delete;
	// skipping one costs a revoked member's access.
	for _, userID := range change.namedUsers() {
		s.cache.InvalidateTeamMemberCache(ctx, userID, change.ProjectID.String())
	}

	// Costs the user a team rather than access: the signup path recreates a
	// missing default on next login. Logged because backfilling legacy teams
	// into projects is where it would start happening in bulk.
	for _, row := range removed {
		if row.IsDefault {
			logger.L().Warn(ctx, "management removed a default team membership",
				logger.WithTeamID(change.ProjectID.String()), logger.WithUserID(row.UserID.String()))
		}
	}

	return nil
}

func (s *Service) applyMembers(ctx context.Context, change MemberChange) ([]authqueries.SyncTeamMembersAbsentRow, error) {
	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start membership transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	exists, err := txDB.TeamExists(ctx, change.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("look up project: %w", err)
	}
	if !exists {
		return nil, ErrProjectNotFound
	}

	if len(change.Present) > 0 {
		// users_teams still points at public.users for both the member and
		// whoever added them, and the caller knows only opaque ids.
		if err := txDB.UpsertPublicUsers(ctx, change.anchoredUsers()); err != nil {
			return nil, fmt.Errorf("anchor users: %w", err)
		}

		if err := txDB.SyncTeamMembersPresent(ctx, authqueries.SyncTeamMembersPresentParams{
			TeamID:  change.ProjectID,
			UserIds: change.Present,
			AddedBy: change.AddedBy,
		}); err != nil {
			return nil, fmt.Errorf("add project members: %w", err)
		}
	}

	var removed []authqueries.SyncTeamMembersAbsentRow
	if len(change.Absent) > 0 {
		removed, err = txDB.SyncTeamMembersAbsent(ctx, authqueries.SyncTeamMembersAbsentParams{
			TeamID:  change.ProjectID,
			UserIds: change.Absent,
		})
		if err != nil {
			return nil, fmt.Errorf("remove project members: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit membership change: %w", err)
	}

	return removed, nil
}

// PurgeUser removes the memberships and access tokens a user holds here.
//
// public.users survives on purpose: addons.added_by would refuse the delete,
// and two created_by columns would quietly null out provenance. Deleting a
// user outright already belongs to the admin route.
func (s *Service) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		return fmt.Errorf("start purge transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Read first, because afterwards nothing says which teams cached this user.
	// That also bounds repair: a crash before the evictions below leaves entries
	// a retry can no longer find, so they stand until they expire. Recording
	// them would be a teardown log, which is a lot for a five-minute worst case.
	teamIDs, err := txDB.ListUserTeamIDs(ctx, userID)
	if err != nil {
		return fmt.Errorf("list user projects: %w", err)
	}

	if err := txDB.PurgeUserMemberships(ctx, userID); err != nil {
		return fmt.Errorf("purge user memberships: %w", err)
	}

	if err := txDB.PurgeUserAccessTokens(ctx, userID); err != nil {
		return fmt.Errorf("purge user access tokens: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit user purge: %w", err)
	}

	for _, teamID := range teamIDs {
		s.cache.InvalidateTeamMemberCache(ctx, userID, teamID.String())
	}

	return nil
}
