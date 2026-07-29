package management

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// defaultProjectTier is the only tier this side ever assigns, and it assigns it
// once. The contract names none: the caller's plan vocabulary is its own, and
// the limits it actually wants arrive absolute through the limits route, which
// team_limits reads in preference to the tier.
const defaultProjectTier = "base_v1"

const teamSlugUniqueConstraint = "teams_slug_unique"

var (
	// ErrProjectSlugImmutable reports a reconcile that would move a project to
	// a different slug.
	ErrProjectSlugImmutable = errors.New("project slug cannot change")

	// ErrProjectSlugTaken reports a slug already held on this cluster, possibly
	// by a project the caller has never heard of.
	ErrProjectSlugTaken = errors.New("project slug is already taken")

	// ErrProjectRaced reports an id inserted between the existence check and
	// this request's own insert.
	ErrProjectRaced = errors.New("project was created concurrently")
)

// Project is the set of properties the caller synchronizes. It sends all of
// them on every push, so a reconcile is a complete statement rather than a
// patch.
type Project struct {
	ID    uuid.UUID
	Name  string
	Slug  string
	Email string
}

// UpsertProject creates a project or reconciles an existing one, reporting
// which happened.
//
// One operation serves both because the caller cannot tell them apart: it
// retries, and a retry after a response it never saw has to land on the same
// state as the original.
func (s *Service) UpsertProject(ctx context.Context, project Project) (stored Project, created bool, err error) {
	stored, created, err = s.writeProject(ctx, project)
	if err != nil {
		return Project{}, false, err
	}

	// A reconcile changes the team, so every cached copy of it is stale — its
	// own entry, each API key's, each member's. A create has nothing cached.
	// Logged rather than returned: the row is committed, and a retry cannot
	// improve on a stale cache.
	if !created {
		if err := s.cache.InvalidateTeamCache(ctx, project.ID); err != nil {
			logger.L().Error(ctx, "invalidating team cache after project reconcile",
				logger.WithTeamID(project.ID.String()), zap.Error(err))
		}
	}

	return stored, created, nil
}

// writeProject branches on whether the project exists, because the two cases
// differ in what they may set: a create assigns the tier, a reconcile writes
// only what the caller synchronizes and leaves the tier alone.
func (s *Service) writeProject(ctx context.Context, project Project) (stored Project, created bool, err error) {
	txDB, tx, err := s.db.WithTx(ctx)
	if err != nil {
		return Project{}, false, fmt.Errorf("start project upsert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := txDB.LockManagedTeam(ctx, project.ID)

	switch {
	case err == nil:
		stored, err = reconcileProject(ctx, txDB, existing, project)
	case dberrors.IsNotFoundError(err):
		stored, err = createProject(ctx, txDB, project)
		created = true
	default:
		return Project{}, false, fmt.Errorf("look up project: %w", err)
	}

	if err != nil {
		return Project{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Project{}, false, fmt.Errorf("commit project upsert: %w", err)
	}

	return stored, created, nil
}

func createProject(ctx context.Context, txDB *authqueries.Queries, project Project) (Project, error) {
	inserted, err := txDB.InsertManagedTeam(ctx, authqueries.InsertManagedTeamParams{
		ID:    project.ID,
		Name:  project.Name,
		Slug:  project.Slug,
		Tier:  defaultProjectTier,
		Email: project.Email,
	})

	switch {
	// ON CONFLICT DO NOTHING yields no row, which here means another request
	// inserted this id between the lock finding nothing and this statement.
	case dberrors.IsNotFoundError(err):
		return Project{}, ErrProjectRaced
	case dberrors.ConstraintName(err) == teamSlugUniqueConstraint:
		return Project{}, fmt.Errorf("%w: %q", ErrProjectSlugTaken, project.Slug)
	case err != nil:
		return Project{}, fmt.Errorf("create project: %w", err)
	}

	return Project{ID: project.ID, Name: inserted.Name, Slug: inserted.Slug, Email: inserted.Email}, nil
}

func reconcileProject(
	ctx context.Context,
	txDB *authqueries.Queries,
	existing authqueries.LockManagedTeamRow,
	project Project,
) (Project, error) {
	// A slug is not a display property, and this side has its own reason to
	// refuse moving one. Template aliases are namespaced by it: register_build
	// stamps the team's slug onto every alias it claims, and a template's name
	// renders as "<slug>/<alias>". Accepting a new slug without rewriting every
	// one of those rows would leave the team's templates addressed under a name
	// that no longer exists. The caller has its own reason too — the slug is the
	// DNS label projects are reached at — but neither is satisfied by a rename
	// here alone.
	//
	// Refusing also stops a reconcile adopting a team it does not own:
	// caller-minted ids will not collide, but backfilling legacy teams into
	// projects makes other ids reachable, and a mismatched slug is the signal
	// that one of them is not the project being described.
	if existing.Slug != project.Slug {
		return Project{}, fmt.Errorf("%w: stored %q, requested %q",
			ErrProjectSlugImmutable, existing.Slug, project.Slug)
	}

	updated, err := txDB.UpdateManagedTeam(ctx, authqueries.UpdateManagedTeamParams{
		ID:    project.ID,
		Name:  project.Name,
		Email: project.Email,
	})
	if err != nil {
		return Project{}, fmt.Errorf("reconcile project: %w", err)
	}

	return Project{ID: project.ID, Name: updated.Name, Slug: updated.Slug, Email: updated.Email}, nil
}
