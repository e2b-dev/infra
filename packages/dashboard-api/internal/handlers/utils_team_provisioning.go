package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	supabasequeries "github.com/e2b-dev/infra/packages/db/pkg/supabase/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	baseTierID                   = "base_v1"
	maxTeamsPerUser              = 3
	maxTeamsPerUserWithProTier   = 10
	teamProvisionRollbackTimeout = 5 * time.Second
)

type provisionedTeam struct {
	ID            uuid.UUID
	Name          string
	Email         string
	Slug          string
	IsBlocked     bool
	BlockedReason *string
}

func (s *APIStore) bootstrapUser(ctx context.Context, userID uuid.UUID) (provisionedTeam, error) {
	authUser, err := s.supabaseDB.Write.GetAuthUserByID(ctx, userID)
	if dberrors.IsNotFoundError(err) {
		return provisionedTeam{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusNotFound,
			Message:    "User not found",
		}
	}
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("get auth user: %w", err)
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := authTxDB.UpsertPublicUser(ctx, authUser.ID); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	// Serialize bootstrap for a user even when they have no team memberships yet.
	if _, err := authTxDB.LockPublicUserForUpdate(ctx, authUser.ID); err != nil {
		return provisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	existingTeam, err := authTxDB.GetDefaultTeamByUserID(ctx, userID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return provisionedTeam{}, fmt.Errorf("commit existing user bootstrap transaction: %w", err)
		}

		req := teamprovision.TeamBillingProvisionRequestedV1{
			TeamID:        existingTeam.ID,
			TeamName:      existingTeam.Name,
			TeamEmail:     existingTeam.Email,
			CreatorUserID: userID,
			Reason:        teamprovision.ReasonDefaultSignupTeam,
		}
		_ = s.teamProvisionSink.ProvisionTeam(ctx, req)

		return provisionedTeam{
			ID:            existingTeam.ID,
			Name:          existingTeam.Name,
			Email:         existingTeam.Email,
			Slug:          existingTeam.Slug,
			IsBlocked:     existingTeam.IsBlocked,
			BlockedReason: existingTeam.BlockedReason,
		}, nil
	}
	if !dberrors.IsNotFoundError(err) {
		return provisionedTeam{}, fmt.Errorf("get default team: %w", err)
	}

	defaultTeamName := defaultTeamNameFromAuthUser(authUser)
	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          defaultTeamName,
		Tier:          baseTierID,
		Email:         authUser.Email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create default team: %w", err)
	}

	if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: true,
		AddedBy:   nil,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("create default team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return provisionedTeam{}, fmt.Errorf("commit user bootstrap transaction: %w", err)
	}

	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        team.ID,
		TeamName:      team.Name,
		TeamEmail:     team.Email,
		CreatorUserID: userID,
		Reason:        teamprovision.ReasonDefaultSignupTeam,
	}
	_ = s.teamProvisionSink.ProvisionTeam(ctx, req)

	return provisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

func (s *APIStore) createTeam(ctx context.Context, userID uuid.UUID, name string) (provisionedTeam, error) {
	authUser, err := s.supabaseDB.Write.GetAuthUserByID(ctx, userID)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("get auth user: %w", err)
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := authTxDB.UpsertPublicUser(ctx, authUser.ID); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	// Serialize team creation even when the user currently has no team memberships.
	if _, err := authTxDB.LockPublicUserForUpdate(ctx, authUser.ID); err != nil {
		return provisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	if err := validateTeamCreationAllowed(ctx, authTxDB, userID); err != nil {
		return provisionedTeam{}, err
	}

	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         authUser.Email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create team: %w", err)
	}

	if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: false,
		AddedBy:   &userID,
	}); err != nil {
		return provisionedTeam{}, fmt.Errorf("create team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return provisionedTeam{}, fmt.Errorf("commit team creation transaction: %w", err)
	}

	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        team.ID,
		TeamName:      team.Name,
		TeamEmail:     team.Email,
		CreatorUserID: userID,
		Reason:        teamprovision.ReasonAdditionalTeam,
	}
	if err := s.teamProvisionSink.ProvisionTeam(ctx, req); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamProvisionRollbackTimeout)
		defer cancel()

		if deleteErr := s.authDB.Write.DeleteTeamByID(rollbackCtx, team.ID); deleteErr != nil {
			return provisionedTeam{}, fmt.Errorf("delete team after provisioning failure: provision=%s delete=%w", err.Error(), deleteErr)
		}

		return provisionedTeam{}, err
	}

	return provisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

func (s *APIStore) createServiceCreatedTeam(ctx context.Context, name string, email string) (provisionedTeam, error) {
	team, err := s.authDB.Write.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create service-created team: %w", err)
	}

	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        team.ID,
		TeamName:      team.Name,
		TeamEmail:     team.Email,
		CreatorUserID: uuid.Nil,
		Reason:        teamprovision.ReasonAdditionalTeam,
	}
	if err := s.teamProvisionSink.ProvisionTeam(ctx, req); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teamProvisionRollbackTimeout)
		defer cancel()

		if deleteErr := s.authDB.Write.DeleteTeamByID(rollbackCtx, team.ID); deleteErr != nil {
			return provisionedTeam{}, fmt.Errorf("delete service-created team after provisioning failure: provision=%s delete=%w", err.Error(), deleteErr)
		}

		return provisionedTeam{}, err
	}

	return provisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

func validateTeamCreationAllowed(ctx context.Context, authTxDB *authqueries.Queries, ownerUserID uuid.UUID) error {
	teams, err := authTxDB.GetTeamsWithUsersTeamsWithTierForUpdate(ctx, ownerUserID)
	if err != nil {
		return fmt.Errorf("query user teams for limit check: %w", err)
	}

	hasProTier := false
	for _, row := range teams {
		if row.Tier != baseTierID {
			hasProTier = true
		}
		if row.IsBanned {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    "You're unable to create a team right now. Please contact support if this persists.",
			}
		}
	}

	if hasProTier {
		if len(teams) >= maxTeamsPerUserWithProTier {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    fmt.Sprintf("You can't create more than %d teams", maxTeamsPerUserWithProTier),
			}
		}
	} else {
		if len(teams) >= maxTeamsPerUser {
			return &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message: fmt.Sprintf(
					"You can't create more than %d teams, you can upgrade to Pro tier to create up to %d teams",
					maxTeamsPerUser,
					maxTeamsPerUserWithProTier,
				),
			}
		}
	}

	return nil
}

func defaultTeamNameFromAuthUser(authUser supabasequeries.AuthUser) string {
	metadata := rawUserMetadata(authUser.RawUserMetaData)

	baseName := firstNonEmpty(
		firstWord(metadataString(metadata, "first_name")),
		firstWord(metadataString(metadata, "firstName")),
		firstWord(metadataString(metadata, "given_name")),
		firstWord(metadataString(metadata, "givenName")),
		firstWord(metadataString(metadata, "name")),
		firstWord(metadataString(metadata, "full_name")),
		firstWord(metadataString(metadata, "fullName")),
		metadataString(metadata, "username"),
		metadataString(metadata, "user_name"),
		metadataString(metadata, "userName"),
		metadataString(metadata, "preferred_username"),
		metadataString(metadata, "preferredUsername"),
		emailPrefix(authUser.Email),
		"User",
	)

	return capitalizeFirstLetter(baseName) + "'s Default Team"
}

func rawUserMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil
	}

	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}

	value, ok := metadata[key].(string)
	if !ok {
		return ""
	}

	return strings.TrimSpace(value)
}

func firstWord(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

func emailPrefix(email string) string {
	prefix, _, _ := strings.Cut(strings.TrimSpace(email), "@")

	return prefix
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func capitalizeFirstLetter(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}

	runes[0] = unicode.ToUpper(runes[0])

	return string(runes)
}

func (s *APIStore) handleProvisioningError(ctx context.Context, c *gin.Context, operation string, err error) {
	attrs := []attribute.KeyValue{
		attribute.String("team.provision.operation", operation),
	}

	var provisionErr *internalteamprovision.ProvisionError
	if errors.As(err, &provisionErr) {
		if provisionErr.StatusCode < http.StatusBadRequest || provisionErr.StatusCode >= 600 {
			telemetry.ReportErrorByCode(ctx, http.StatusInternalServerError, operation+" failed", err, attrs...)
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)

			return
		}

		telemetry.ReportErrorByCode(ctx, provisionErr.StatusCode, operation+" failed", err, attrs...)
		s.sendAPIStoreError(c, provisionErr.StatusCode, provisionErr.Error())

		return
	}

	telemetry.ReportErrorByCode(ctx, http.StatusInternalServerError, operation+" failed", err, attrs...)
	s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to "+operation)
}
