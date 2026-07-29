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
// interface starts. project_type does not decide it: the caller names tiers
// this cluster has never heard of, so mapping it onto teams.tier would fail
// the foreign key on the first project that is not base.
const managedProjectTier = "base_v1"

const teamSlugUniqueConstraint = "teams_slug_unique"

var (
	// errProjectSlugImmutable reports a reconcile that would move a project to
	// a different slug.
	errProjectSlugImmutable = errors.New("project slug cannot change")

	// errProjectEmailRequired reports a create with no address to store.
	errProjectEmailRequired = errors.New("email is required to create a project")
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

	body, err := ginutils.ParseBody[api.ManagementProjectUpsertRequest](ctx, c)
	if err != nil {
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project failed",
			fmt.Errorf("parse project upsert request: %w", err), attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	// Traced and not stored. The contract says project_type is recorded rather
	// than interpreted, and nothing here reads it: no column, no behaviour, and
	// limits that arrive already resolved.
	attrs = append(attrs, attribute.String("project.type", body.ProjectType))
	telemetry.SetAttributes(ctx, attrs...)

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
		Id:          teamID,
		Name:        project.Name,
		Slug:        project.Slug,
		ProjectType: body.ProjectType,
		Email:       optionalEmail(project.Email),
	})
}

// managedProject is the part of a project both branches return.
type managedProject struct {
	Name  string
	Slug  string
	Email string
}

// upsertManagedProject inserts first and reconciles when the id is taken, so
// the common create path costs one statement.
//
// The address is required to create and optional to reconcile, which the
// contract cannot express on a single operation. A create has nowhere to get
// one from and the column does not accept none; a reconcile already has one
// stored, and blanking it because the caller stopped sending it would discard
// something nobody asked to change.
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

	if body.Email != nil {
		inserted, insertErr := txDB.InsertManagedTeam(ctx, authqueries.InsertManagedTeamParams{
			ID:    teamID,
			Name:  body.Name,
			Slug:  body.Slug,
			Tier:  managedProjectTier,
			Email: *body.Email,
		})

		if insertErr == nil {
			if err := tx.Commit(ctx); err != nil {
				return managedProject{}, false, fmt.Errorf("commit project create: %w", err)
			}

			return managedProject{Name: inserted.Name, Slug: inserted.Slug, Email: inserted.Email}, true, nil
		}

		// No row means the id is taken, so this is a reconcile: DO NOTHING lets
		// the insert double as the branch test, and a concurrent create resolve
		// below instead of into a 409. Anything else is a real failure.
		if !dberrors.IsNotFoundError(insertErr) {
			return managedProject{}, false, fmt.Errorf("create project: %w", insertErr)
		}
	}

	existing, err := txDB.LockManagedTeam(ctx, teamID)
	if err != nil {
		// Only reachable without an address, since the insert above would
		// otherwise have created the row.
		if dberrors.IsNotFoundError(err) {
			return managedProject{}, false, errProjectEmailRequired
		}

		return managedProject{}, false, fmt.Errorf("lock project: %w", err)
	}

	// The rule that a slug never moves is the caller's: it owns the region-wide
	// namespace these labels address, and teams_slug_unique is only the backstop
	// underneath it. Refusing also stops a reconcile adopting a team it does not
	// own — caller-minted ids will not collide, but backfilling legacy teams
	// into projects makes other ids reachable, and a mismatched slug is the
	// signal that one of them is not the project being described.
	if existing.Slug != body.Slug {
		return managedProject{}, false, fmt.Errorf("%w: stored %q, requested %q",
			errProjectSlugImmutable, existing.Slug, body.Slug)
	}

	updated, err := txDB.UpdateManagedTeam(ctx, authqueries.UpdateManagedTeamParams{
		ID:    teamID,
		Name:  body.Name,
		Email: body.Email,
	})
	if err != nil {
		return managedProject{}, false, fmt.Errorf("reconcile project: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return managedProject{}, false, fmt.Errorf("commit project reconcile: %w", err)
	}

	return managedProject{Name: updated.Name, Slug: updated.Slug, Email: updated.Email}, false, nil
}

func (s *APIStore) sendProjectUpsertError(c *gin.Context, err error, attrs ...attribute.KeyValue) {
	ctx := c.Request.Context()

	switch {
	case errors.Is(err, errProjectEmailRequired):
		telemetry.ReportErrorByCode(ctx, http.StatusBadRequest, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusBadRequest, "Email is required to create a project")

	case errors.Is(err, errProjectSlugImmutable):
		telemetry.ReportErrorByCode(ctx, http.StatusConflict, "upsert project failed", err, attrs...)
		s.sendAPIStoreError(c, http.StatusConflict, "Project slug cannot change")

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

// optionalEmail reports an unset address as absent rather than blank.
func optionalEmail(email string) *string {
	if email == "" {
		return nil
	}

	return &email
}
