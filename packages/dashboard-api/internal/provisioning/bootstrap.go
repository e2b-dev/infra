package provisioning

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/identity"
	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func (s *Service) BootstrapOIDCUser(ctx context.Context, input OIDCUserBootstrapInput) (ProvisionedTeam, error) {
	idp, err := s.providerForIssuer(input.OIDCIssuer)
	if err != nil {
		return ProvisionedTeam{}, err
	}

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

	return s.bootstrapUser(ctx, idp, profile, bootstrapUserIdentity{
		Issuer:  input.OIDCIssuer,
		Subject: input.OIDCUserID,
	})
}

func (s *Service) providerForIssuer(issuer string) (identity.Provider, error) {
	oryIssuer := strings.TrimSpace(s.issuerURL)

	if s.idp != nil && oryIssuer != "" && oryIssuer == strings.TrimSpace(issuer) {
		return s.idp, nil
	}

	return nil, &internalteamprovision.ProvisionError{
		StatusCode: http.StatusBadRequest,
		Message:    "oidc_issuer does not match a configured identity provider",
	}
}

func (s *Service) bootstrapUser(ctx context.Context, idp identity.Provider, profile bootstrapUserProfile, oidcIdentity bootstrapUserIdentity) (ProvisionedTeam, error) {
	ssoOrgID, err := idp.GetIdentityOrganizationID(ctx, oidcIdentity.Subject)
	if err != nil {
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

		if err := backfillIdentityExternalID(ctx, idp, oidcIdentity.Subject, profile.UserID); err != nil {
			return ProvisionedTeam{}, err
		}

		return newProvisionedTeam(existingTeam.ID, existingTeam.Name, existingTeam.Email, existingTeam.Slug, existingTeam.IsBlocked, existingTeam.BlockedReason), nil
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

		if err := backfillIdentityExternalID(ctx, idp, oidcIdentity.Subject, profile.UserID); err != nil {
			return ProvisionedTeam{}, err
		}

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

	if err := backfillIdentityExternalID(ctx, idp, oidcIdentity.Subject, profile.UserID); err != nil {
		return ProvisionedTeam{}, err
	}

	return newProvisionedTeam(team.ID, team.Name, team.Email, team.Slug, team.IsBlocked, team.BlockedReason), nil
}

func backfillIdentityExternalID(ctx context.Context, idp identity.Provider, subject string, userID uuid.UUID) error {
	if err := idp.SetIdentityExternalID(ctx, subject, userID); err != nil {
		return fmt.Errorf("set ory identity external id: %w", err)
	}

	return nil
}
