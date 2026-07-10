package provisioning

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func (s *Service) BootstrapOIDCUser(ctx context.Context, input OIDCUserBootstrapInput) (ProvisionedTeam, error) {
	profile := bootstrapUserProfile{
		UserID:          uuid.New(),
		Email:           input.OIDCUserEmail,
		DefaultTeamName: defaultTeamNameFromOIDCUserName(input.OIDCUserName),
		CreatorContext: normalizeCreatorContext(&teamprovision.CreatorContextV1{
			IPAddress:  input.SignupIP,
			UserAgent:  input.SignupUserAgent,
			AuthMethod: teamprovision.AuthMethodSocial,
		}),
	}

	return s.bootstrapUser(ctx, profile, bootstrapUserIdentity{
		Issuer:  strings.TrimSpace(input.OIDCIssuer),
		Subject: input.OIDCUserID,
	})
}

func (s *Service) bootstrapUser(ctx context.Context, profile bootstrapUserProfile, oidcIdentity bootstrapUserIdentity) (ProvisionedTeam, error) {
	ssoOrgID, err := s.identityService.IdentityOrganizationID(ctx, oidcIdentity.Issuer, oidcIdentity.Subject)
	if err != nil {
		if errors.Is(err, identity.ErrUnknownIssuer) {
			return ProvisionedTeam{}, &internalteamprovision.ProvisionError{
				StatusCode: http.StatusBadRequest,
				Message:    "oidc_issuer does not match a configured identity provider",
			}
		}

		return ProvisionedTeam{}, fmt.Errorf("resolve sso organization: %w", err)
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := authTxDB.GetUserIdentity(ctx, authqueries.GetUserIdentityParams{
		OidcIss: oidcIdentity.Issuer,
		OidcSub: oidcIdentity.Subject,
	})
	switch {
	case err == nil:
		profile.UserID = existing.UserID
	case !dberrors.IsNotFoundError(err):
		return ProvisionedTeam{}, fmt.Errorf("get user identity: %w", err)
	default:
		// No identity found: check if a pre-Ory user with this email exists and
		// has not yet been linked to any identity for this issuer. If so, reuse
		// their user_id so their existing teams and data are preserved on first login.
		if profile.Email != "" {
			emailMatches, emailErr := s.authDB.FindUserIDsByEmail(ctx, profile.Email)
			if emailErr != nil {
				return ProvisionedTeam{}, fmt.Errorf("find pre-Ory user by email: %w", emailErr)
			}
			// Only attempt email-based migration when there is exactly one match.
			// Multiple matches indicate email reuse across accounts; skip to avoid
			// linking the wrong user_id.
			if len(emailMatches) == 1 {
				linkedUserID := emailMatches[0]
				// Lock the candidate user row before re-checking identities. Without
				// this lock, two concurrent bootstraps with different subjects but the
				// same email can both observe zero identities and both claim linkedUserID.
				if _, lockErr := authTxDB.LockPublicUserForUpdate(ctx, linkedUserID); lockErr != nil {
					return ProvisionedTeam{}, fmt.Errorf("lock pre-Ory user for migration: %w", lockErr)
				}
				identityRows, idErr := authTxDB.GetUserIdentitiesByUserIDsAndIssuers(ctx, authqueries.GetUserIdentitiesByUserIDsAndIssuersParams{
					OidcIssuers: []string{oidcIdentity.Issuer},
					UserIds:     []uuid.UUID{linkedUserID},
				})
				if idErr != nil {
					return ProvisionedTeam{}, fmt.Errorf("check existing identities for pre-Ory user: %w", idErr)
				}
				// Only reuse the pre-Ory account for non-SSO identities.
				// SSO-backed identities must go through enrollSSOMember so the
				// user is added to the correct org team; skipping that step
				// would return the user's personal default team and silently
				// drop the SSO auto-join memberships.
				if len(identityRows) == 0 && ssoOrgID == uuid.Nil {
					profile.UserID = linkedUserID
				}
			}
		}
	}

	candidateUserID := profile.UserID
	if err := authTxDB.UpsertPublicUser(ctx, candidateUserID); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}
	canonicalUserID, err := authTxDB.UpsertPublicIdentity(ctx, authqueries.UpsertPublicIdentityParams{
		OidcIss: oidcIdentity.Issuer,
		OidcSub: oidcIdentity.Subject,
		UserID:  candidateUserID,
	})
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("upsert public identity: %w", err)
	}
	if canonicalUserID != candidateUserID {
		if err := authTxDB.DeletePublicUser(ctx, candidateUserID); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("delete orphan public user: %w", err)
		}
		profile.UserID = canonicalUserID
	}

	if _, err := authTxDB.LockPublicUserForUpdate(ctx, profile.UserID); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("lock public user: %w", err)
	}

	existingTeam, err := authTxDB.GetDefaultTeamByUserID(ctx, profile.UserID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("commit existing user bootstrap transaction: %w", err)
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
			_ = s.billing.ProvisionTeam(ctx, req)
		}

		if err := backfillIdentityExternalID(ctx, s.identityService, oidcIdentity, profile.UserID); err != nil {
			logger.L().Warn(ctx, "failed to backfill identity external_id (recoverable)",
				zap.String("user_id", profile.UserID.String()),
				zap.Error(err),
			)
		}

		return newProvisionedTeam(existingTeam.ID, existingTeam.Name, existingTeam.Email, existingTeam.Slug, existingTeam.IsBlocked, existingTeam.BlockedReason, profile.UserID), nil
	}
	if !dberrors.IsNotFoundError(err) {
		return ProvisionedTeam{}, fmt.Errorf("get default team: %w", err)
	}

	if ssoOrgID != uuid.Nil {
		landing, err := s.enrollSSOMember(ctx, authTxDB, profile.UserID, ssoOrgID)
		if err != nil {
			return ProvisionedTeam{}, err
		}

		if err := tx.Commit(ctx); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("commit sso bootstrap transaction: %w", err)
		}

		if err := backfillIdentityExternalID(ctx, s.identityService, oidcIdentity, profile.UserID); err != nil {
			logger.L().Warn(ctx, "failed to backfill identity external_id (recoverable)",
				zap.String("user_id", profile.UserID.String()),
				zap.Error(err),
			)
		}

		landing.UserID = profile.UserID

		return landing, nil
	}

	team, err := authTxDB.CreateTeam(ctx, authqueries.CreateTeamParams{
		Name:          profile.DefaultTeamName,
		Tier:          baseTierID,
		Email:         profile.Email,
		IsBlocked:     false,
		BlockedReason: nil,
	})
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("create default team: %w", err)
	}

	if err := authTxDB.CreateTeamMembership(ctx, authqueries.CreateTeamMembershipParams{
		UserID:    profile.UserID,
		TeamID:    team.ID,
		IsDefault: true,
		AddedBy:   nil,
	}); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("create default team membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("commit user bootstrap transaction: %w", err)
	}

	// Emit the billing event before the external_id backfill: ProvisionTeam is
	// fire-and-forget, but the backfill returns early on failure, and the
	// existing-team recovery path only re-fires ProvisionTeam within
	// bootstrapProvisionRetryAge of team creation. Provisioning first guarantees the
	// freshly committed team is never left billing-orphaned by an Ory PATCH failure.
	req := teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:         team.ID,
		TeamName:       team.Name,
		TeamEmail:      team.Email,
		CreatorUserID:  profile.UserID,
		CreatorContext: s.teamCreatorContextForProvisioning(ctx, profile),
		Reason:         teamprovision.ReasonDefaultSignupTeam,
	}
	_ = s.billing.ProvisionTeam(ctx, req)

	if err := backfillIdentityExternalID(ctx, s.identityService, oidcIdentity, profile.UserID); err != nil {
		logger.L().Warn(ctx, "failed to backfill identity external_id (recoverable)",
			zap.String("user_id", profile.UserID.String()),
			zap.Error(err),
		)
	}

	return newProvisionedTeam(team.ID, team.Name, team.Email, team.Slug, team.IsBlocked, team.BlockedReason, profile.UserID), nil
}

func backfillIdentityExternalID(ctx context.Context, identityService identity.Service, oidcIdentity bootstrapUserIdentity, userID uuid.UUID) error {
	if err := identityService.SetIdentityExternalID(ctx, oidcIdentity.Issuer, oidcIdentity.Subject, userID); err != nil {
		return fmt.Errorf("set identity external id: %w", err)
	}

	return nil
}
