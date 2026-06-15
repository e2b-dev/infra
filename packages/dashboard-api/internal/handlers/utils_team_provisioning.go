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
	"go.uber.org/zap"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	baseTierID                   = "base_v1"
	maxTeamsPerUser              = 3
	maxTeamsPerUserWithProTier   = 10
	bootstrapProvisionRetryAge   = 30 * time.Second
	teamProvisionRollbackTimeout = 5 * time.Second
	creatorContextResolveTimeout = 2 * time.Second
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
	CreatorContext  *teamprovision.CreatorContextV1
}

type bootstrapUserIdentity struct {
	Issuer  string
	Subject string
}

type oidcUserBootstrapInput struct {
	OIDCIssuer      string
	OIDCUserID      string
	OIDCUserEmail   string
	OIDCUserName    *string
	SignupIP        string
	SignupUserAgent string
}

func (s *APIStore) bootstrapSupabaseUser(ctx context.Context, userID uuid.UUID) (provisionedTeam, error) {
	profile, err := s.bootstrapUserProfileFromSupabase(ctx, userID)
	if err != nil {
		return provisionedTeam{}, err
	}

	return s.bootstrapUser(ctx, profile)
}

func (s *APIStore) bootstrapUserProfileFromSupabase(ctx context.Context, userID uuid.UUID) (bootstrapUserProfile, error) {
	profile, err := s.resolveProfile(ctx, userID)
	if err != nil {
		return bootstrapUserProfile{}, err
	}

	return bootstrapUserProfile{
		UserID:          userID,
		Email:           profile.Email,
		DefaultTeamName: defaultTeamNameFromProfile(profile),
	}, nil
}

// resolveProfile fetches a single user's profile through the configured profile
// provider, returning a 404 ProvisionError when the user is unknown. This keeps
// provisioning independent of which backend (Supabase or Ory) owns the user.
func (s *APIStore) resolveProfile(ctx context.Context, userID uuid.UUID) (userprofile.Profile, error) {
	profiles, err := s.userProfiles.GetProfilesByUserID(ctx, []uuid.UUID{userID})
	if err != nil {
		return userprofile.Profile{}, fmt.Errorf("get user profile: %w", err)
	}

	profile, ok := profiles[userID]
	if !ok {
		return userprofile.Profile{}, &internalteamprovision.ProvisionError{
			StatusCode: http.StatusNotFound,
			Message:    "User not found",
		}
	}

	return profile, nil
}

func (s *APIStore) bootstrapOIDCUser(ctx context.Context, input oidcUserBootstrapInput) (provisionedTeam, error) {
	if err := s.requireConfiguredOIDCIssuer(input.OIDCIssuer); err != nil {
		return provisionedTeam{}, err
	}

	profile := bootstrapUserProfile{
		UserID:          uuid.New(),
		Email:           input.OIDCUserEmail,
		DefaultTeamName: defaultTeamNameFromOIDCUserName(input.OIDCUserName),
		CreatorContext:  creatorContextFromSignupMetadata(input.SignupIP, input.SignupUserAgent, teamprovision.AuthMethodSocial),
	}

	return s.bootstrapUserWithIdentity(ctx, profile, &bootstrapUserIdentity{
		Issuer:  input.OIDCIssuer,
		Subject: input.OIDCUserID,
	})
}

// requireConfiguredOIDCIssuer rejects bootstrap requests whose issuer is not in
// the configured provider list. Without this an admin-token holder could plant
// an identity under any arbitrary iss string. When the user-profile provider
// requires Ory, only ORY_ISSUER_URL is accepted: the Ory resolver looks up
// public.user_identities by exactly that issuer, so any other configured JWT
// issuer would create rows that profile/membership lookups never read.
func (s *APIStore) requireConfiguredOIDCIssuer(issuer string) error {
	oryIssuer := strings.TrimSpace(s.config.OryIssuerURL)

	if s.config.UserProfileProvider.RequiresOry() {
		if oryIssuer != "" && oryIssuer == issuer {
			return nil
		}

		return &internalteamprovision.ProvisionError{
			StatusCode: http.StatusBadRequest,
			Message:    "oidc_issuer must equal the configured ORY_ISSUER_URL",
		}
	}

	for _, jwt := range s.config.AuthProvider.JWT {
		if strings.TrimSpace(jwt.Issuer.URL) == issuer {
			return nil
		}
	}
	if oryIssuer != "" && oryIssuer == issuer {
		return nil
	}

	return &internalteamprovision.ProvisionError{
		StatusCode: http.StatusBadRequest,
		Message:    "oidc_issuer is not a configured auth provider",
	}
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

		if time.Since(existingTeam.CreatedAt) < bootstrapProvisionRetryAge {
			req := teamprovision.TeamBillingProvisionRequestedV1{
				TeamID:         existingTeam.ID,
				TeamName:       existingTeam.Name,
				TeamEmail:      existingTeam.Email,
				CreatorUserID:  profile.UserID,
				CreatorContext: s.teamCreatorContextForProvisioning(ctx, profile),
				Reason:         teamprovision.ReasonDefaultSignupTeam,
			}
			_ = s.teamProvisionSink.ProvisionTeam(ctx, req)
		}

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
		TeamID:         team.ID,
		TeamName:       team.Name,
		TeamEmail:      team.Email,
		CreatorUserID:  profile.UserID,
		CreatorContext: s.teamCreatorContextForProvisioning(ctx, profile),
		Reason:         teamprovision.ReasonDefaultSignupTeam,
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
	profile, err := s.resolveProfile(ctx, userID)
	if err != nil {
		return provisionedTeam{}, err
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := authTxDB.UpsertPublicUser(ctx, userID); err != nil {
		return provisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}

	// Serialize team creation even when the user currently has no team memberships.
	if _, err := authTxDB.LockPublicUserForUpdate(ctx, userID); err != nil {
		return provisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	if err := validateTeamCreationAllowed(ctx, authTxDB, userID); err != nil {
		return provisionedTeam{}, err
	}

	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         profile.Email,
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
		TeamID:         team.ID,
		TeamName:       team.Name,
		TeamEmail:      team.Email,
		CreatorUserID:  userID,
		CreatorContext: s.resolveTeamCreatorContext(ctx, userID),
		Reason:         teamprovision.ReasonAdditionalTeam,
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

func (s *APIStore) bootstrapTeam(ctx context.Context, name string, email string) (provisionedTeam, error) {
	team, err := s.authDB.Write.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          name,
		Tier:          baseTierID,
		Email:         email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return provisionedTeam{}, fmt.Errorf("create team: %w", err)
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

func (s *APIStore) resolveTeamCreatorContext(ctx context.Context, userID uuid.UUID) *teamprovision.CreatorContextV1 {
	if userID == uuid.Nil || s.userProfiles == nil {
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, creatorContextResolveTimeout)
	defer cancel()

	creatorContext, err := s.userProfiles.GetTeamCreatorContext(resolveCtx, userID)
	if err != nil {
		logger.L().Warn(ctx, "failed to resolve creator context for team provisioning",
			zap.String("user_id", userID.String()),
			zap.Error(err),
		)

		return nil
	}

	return normalizeCreatorContext(creatorContext)
}

func (s *APIStore) teamCreatorContextForProvisioning(ctx context.Context, profile bootstrapUserProfile) *teamprovision.CreatorContextV1 {
	if profile.CreatorContext != nil {
		return normalizeCreatorContext(profile.CreatorContext)
	}

	return s.resolveTeamCreatorContext(ctx, profile.UserID)
}

func creatorContextFromSignupMetadata(signupIP, signupUserAgent, authMethod string) *teamprovision.CreatorContextV1 {
	return normalizeCreatorContext(&teamprovision.CreatorContextV1{
		IPAddress:  signupIP,
		UserAgent:  signupUserAgent,
		AuthMethod: authMethod,
	})
}

func normalizeCreatorContext(creatorContext *teamprovision.CreatorContextV1) *teamprovision.CreatorContextV1 {
	if creatorContext == nil {
		return nil
	}

	ipAddress := strings.TrimSpace(creatorContext.IPAddress)
	if ipAddress == "" {
		return nil
	}

	return &teamprovision.CreatorContextV1{
		IPAddress:  ipAddress,
		UserAgent:  strings.TrimSpace(creatorContext.UserAgent),
		AuthMethod: strings.TrimSpace(creatorContext.AuthMethod),
	}
}

func defaultTeamNameFromProfile(profile userprofile.Profile) string {
	baseName := utils.FirstNonEmpty(
		firstWord(profile.Name),
		emailPrefix(profile.Email),
	)
	if baseName == "" {
		return "Default Team"
	}

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
