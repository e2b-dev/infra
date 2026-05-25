package handlers

import (
	"context"
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
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
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

type bootstrapUserProfile struct {
	UserID          uuid.UUID
	Email           string
	DefaultTeamName string
}

type bootstrapUserIdentity struct {
	Issuer  string
	Subject string
}

type oidcUserBootstrapInput struct {
	OIDCUserID    string
	OIDCUserEmail string
	OIDCUserName  *string
}

func (s *APIStore) bootstrapSupabaseUser(ctx context.Context, userID uuid.UUID) (provisionedTeam, error) {
	profile, err := s.bootstrapUserProfileFromSupabase(ctx, userID)
	if err != nil {
		return provisionedTeam{}, err
	}

	return s.bootstrapUser(ctx, profile)
}

func (s *APIStore) bootstrapUserProfileFromSupabase(ctx context.Context, userID uuid.UUID) (bootstrapUserProfile, error) {
	profiles, err := s.userProfiles.GetProfilesByUserID(ctx, []uuid.UUID{userID})
	if err != nil {
		return bootstrapUserProfile{}, fmt.Errorf("get user profile: %w", err)
	}

	profile, ok := profiles[userID]
	if !ok {
		return bootstrapUserProfile{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusNotFound,
			Message:    "User not found",
		}
	}

	return bootstrapUserProfile{
		UserID:          userID,
		Email:           profile.Email,
		DefaultTeamName: defaultTeamNameFromProfile(profile),
	}, nil
}

func (s *APIStore) bootstrapOIDCUser(ctx context.Context, input oidcUserBootstrapInput) (provisionedTeam, error) {
	issuer, err := s.oidcIssuer()
	if err != nil {
		return provisionedTeam{}, err
	}

	profile := bootstrapUserProfile{
		UserID:          uuid.New(),
		Email:           input.OIDCUserEmail,
		DefaultTeamName: defaultTeamNameFromOIDCUserName(input.OIDCUserName),
	}

	return s.bootstrapUserWithIdentity(ctx, profile, &bootstrapUserIdentity{
		Issuer:  issuer,
		Subject: input.OIDCUserID,
	})
}

func (s *APIStore) oidcIssuer() (string, error) {
	if len(s.config.AuthProvider.JWT) != 1 {
		return "", &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadRequest,
			Message:    "Expected exactly one OIDC auth provider issuer",
		}
	}
	issuer := strings.TrimSpace(s.config.AuthProvider.JWT[0].Issuer.URL)
	if issuer == "" {
		return "", &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadRequest,
			Message:    "OIDC auth provider issuer is not configured",
		}
	}

	return issuer, nil
}

func (s *APIStore) bootstrapUser(ctx context.Context, profile bootstrapUserProfile) (provisionedTeam, error) {
	return s.bootstrapUserWithIdentity(ctx, profile, nil)
}

func (s *APIStore) bootstrapUserWithIdentity(ctx context.Context, profile bootstrapUserProfile, identity *bootstrapUserIdentity) (provisionedTeam, error) {
	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if identity != nil {
		existing, err := authTxDB.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
			OidcIss: identity.Issuer,
			OidcSub: identity.Subject,
		})
		switch {
		case err == nil:
			profile.UserID = existing.UserID
		case !dberrors.IsNotFoundError(err):
			return provisionedTeam{}, fmt.Errorf("get user identity: %w", err)
		}
	}

	candidateUserID := profile.UserID
	if err := authTxDB.UpsertPublicUser(ctx, candidateUserID); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}
	if identity != nil {
		canonicalUserID, err := authTxDB.UpsertPublicIdentity(ctx, authqueries.UpsertPublicIdentityParams{
			OidcIss: identity.Issuer,
			OidcSub: identity.Subject,
			UserID:  candidateUserID,
		})
		if err != nil {
			return provisionedTeam{}, fmt.Errorf("upsert public identity: %w", err)
		}
		if canonicalUserID != candidateUserID {
			// concurrent bootstrap claimed the identity first; drop the orphan candidate row
			if err := authTxDB.DeletePublicUser(ctx, candidateUserID); err != nil {
				return provisionedTeam{}, fmt.Errorf("delete orphan public user: %w", err)
			}
			profile.UserID = canonicalUserID
		}
	}

	// Serialize bootstrap for a user even when they have no team memberships yet.
	if _, err := authTxDB.LockPublicUserForUpdate(ctx, profile.UserID); err != nil {
		return provisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	existingTeam, err := authTxDB.GetDefaultTeamByUserID(ctx, profile.UserID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return provisionedTeam{}, fmt.Errorf("commit existing user bootstrap transaction: %w", err)
		}

		req := teamprovision.TeamBillingProvisionRequestedV1{
			TeamID:        existingTeam.ID,
			TeamName:      existingTeam.Name,
			TeamEmail:     existingTeam.Email,
			CreatorUserID: profile.UserID,
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

	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          profile.DefaultTeamName,
		Tier:          baseTierID,
		Email:         profile.Email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create default team: %w", err)
	}

	if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID:    profile.UserID,
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
		CreatorUserID: profile.UserID,
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

func defaultTeamNameFromProfile(profile userprofile.Profile) string {
	baseName := userprofile.FirstNonEmpty(
		firstWord(profile.Name),
		emailPrefix(profile.Email),
		"User",
	)

	return capitalizeFirstLetter(baseName) + "'s Default Team"
}

func defaultTeamNameFromOIDCUserName(name *string) string {
	if name == nil || strings.TrimSpace(*name) == "" {
		return "Default Team"
	}

	return capitalizeFirstLetter(firstWord(*name)) + "'s Default Team"
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
