package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// managedProjectTier is where a project created through the management
// interface starts, and the only tier this side ever assigns. Nothing in the
// contract names one: the caller's plan vocabulary is its own, and the limits
// it actually wants arrive absolute through upsertProjectLimits, which
// team_limits reads in preference to the tier.
const managedProjectTier = "base_v1"

const teamSlugUniqueConstraint = "teams_slug_unique"

var (
	// errProjectSlugImmutable reports a reconcile that would move a project to
	// a different slug.
	errProjectSlugImmutable = errors.New("project slug cannot change")

	// errProjectRaced reports an id that appeared between the existence check
	// and the insert.
	errProjectRaced = errors.New("project was created concurrently")
)

// ManagementUpsertProject creates or reconciles a project from a
// caller-supplied id.
//
// One request serves both because the caller cannot tell them apart: it
// retries, and a retry after a response it never saw has to land on the same
// state as the original.
func (s *APIStore) ManagementUpsertProject(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	attrs := []attribute.KeyValue{telemetry.WithTeamID(teamID.String())}
	telemetry.SetAttributes(ctx, attrs...)

	body, err := ginutils.ParseBody[api.ManagementProjectUpsertRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project failed",
			fmt.Errorf("parse project upsert request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	project, created, err := s.upsertManagedProject(ctx, teamID, body)
	if err != nil {
		s.sendProjectUpsertError(c, err, attrs...)

		return
	}

	// A reconcile changes the team, so every cached copy of it is stale — its
	// own entry, each API key's, each member's. Logged rather than returned:
	// the row is committed, and a retry cannot improve on a stale cache.
	if !created {
		if err := s.authService.InvalidateTeamCache(ctx, teamID); err != nil {
			logger.L().Error(ctx, "invalidating team cache after project reconcile",
				logger.WithTeamID(teamID.String()), zap.Error(err))
		}
	}

	c.JSON(upsertStatus(created), api.ManagementProject{
		Id:    teamID,
		Name:  project.Name,
		Slug:  project.Slug,
		Email: project.Email,
	})
}

// managedProject is the part of a project both branches return.
type managedProject struct {
	Name  string
	Slug  string
	Email string
}

// upsertManagedProject branches on whether the project already exists, because
// the two cases differ in what they are allowed to set. A create assigns the
// tier; a reconcile writes only the properties the caller synchronizes and
// leaves the tier where it is.
func (s *APIStore) upsertManagedProject(
	ctx context.Context,
	teamID api.TeamID,
	body api.ManagementProjectUpsertRequest,
) (project managedProject, created bool, err error) {
	txDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return managedProject{}, false, fmt.Errorf("start project upsert transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := txDB.LockManagedTeam(ctx, teamID)

	switch {
	case err == nil:
		project, err = reconcileManagedProject(ctx, txDB, teamID, existing, body)
	case dberrors.IsNotFoundError(err):
		project, err = createManagedProject(ctx, txDB, teamID, body)
		created = true
	default:
		return managedProject{}, false, fmt.Errorf("look up project: %w", err)
	}

	if err != nil {
		return managedProject{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return managedProject{}, false, fmt.Errorf("commit project upsert: %w", err)
	}

	return project, created, nil
}

func createManagedProject(
	ctx context.Context,
	txDB *authqueries.Queries,
	teamID api.TeamID,
	body api.ManagementProjectUpsertRequest,
) (managedProject, error) {
	inserted, err := txDB.InsertManagedTeam(ctx, authqueries.InsertManagedTeamParams{
		ID:    teamID,
		Name:  body.Name,
		Slug:  body.Slug,
		Tier:  managedProjectTier,
		Email: body.Email,
	})

	switch {
	// ON CONFLICT DO NOTHING yields no row, which here means another request
	// inserted this id between the lock finding nothing and this statement.
	case dberrors.IsNotFoundError(err):
		return managedProject{}, errProjectRaced
	case err != nil:
		return managedProject{}, fmt.Errorf("create project: %w", err)
	}

	return managedProject{Name: inserted.Name, Slug: inserted.Slug, Email: inserted.Email}, nil
}

func reconcileManagedProject(
	ctx context.Context,
	txDB *authqueries.Queries,
	teamID api.TeamID,
	existing authqueries.LockManagedTeamRow,
	body api.ManagementProjectUpsertRequest,
) (managedProject, error) {
	// The rule that a slug never moves is the caller's: it owns the region-wide
	// namespace these labels address, and teams_slug_unique is only the backstop
	// underneath it. Refusing also stops a reconcile adopting a team it does not
	// own — caller-minted ids will not collide, but backfilling legacy teams
	// into projects makes other ids reachable, and a mismatched slug is the
	// signal that one of them is not the project being described.
	if existing.Slug != body.Slug {
		return managedProject{}, fmt.Errorf("%w: stored %q, requested %q",
			errProjectSlugImmutable, existing.Slug, body.Slug)
	}

	updated, err := txDB.UpdateManagedTeam(ctx, authqueries.UpdateManagedTeamParams{
		ID:    teamID,
		Name:  body.Name,
		Email: body.Email,
	})
	if err != nil {
		return managedProject{}, fmt.Errorf("reconcile project: %w", err)
	}

	return managedProject{Name: updated.Name, Slug: updated.Slug, Email: updated.Email}, nil
}

func (s *APIStore) sendProjectUpsertError(c *gin.Context, err error, attrs ...attribute.KeyValue) {
	ctx := c.Request.Context()

	switch {
	case errors.Is(err, errProjectSlugImmutable):
		telemetry.ReportErrorByCode(ctx, http.StatusConflict, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusConflict, "Project slug cannot change")

	case errors.Is(err, errProjectRaced):
		telemetry.ReportErrorByCode(ctx, http.StatusConflict, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusConflict, "Project was created concurrently, retry")

	// Slugs are unique cluster-wide, so the collision may be with a project the
	// caller has never heard of. Only it can pick another name.
	case dberrors.ConstraintName(err) == teamSlugUniqueConstraint:
		telemetry.ReportErrorByCode(ctx, http.StatusConflict, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusConflict, "Slug is already taken on this control plane")

	default:
		telemetry.ReportCriticalError(ctx, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Error upserting project")
	}
}

// upsertStatus distinguishes a project this request created from one it found.
func upsertStatus(created bool) int {
	if created {
		return http.StatusCreated
	}

	return http.StatusOK
}
