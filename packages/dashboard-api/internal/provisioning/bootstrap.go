package provisioning

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func (s *Service) BootstrapOIDCUser(ctx context.Context, input OIDCUserBootstrapInput) (ProvisionedTeam, error) {
	if err := s.requireConfiguredOIDCIssuer(input.OIDCIssuer); err != nil {
		return ProvisionedTeam{}, err
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

// requireConfiguredOIDCIssuer rejects bootstrap requests whose issuer is not the
// configured Ory issuer.
func (s *Service) requireConfiguredOIDCIssuer(issuer string) error {
	oryIssuer := strings.TrimSpace(s.issuerURL)

	if oryIssuer != "" && oryIssuer == issuer {
		return nil
	}

	return &internalteamprovision.ProvisionError{
		StatusCode: http.StatusBadRequest,
		Message:    "oidc_issuer must equal the configured ORY_ISSUER_URL",
	}
}

func (s *Service) bootstrapUserWithIdentity(ctx context.Context, profile bootstrapUserProfile, identity *bootstrapUserIdentity) (ProvisionedTeam, error) {
	// Resolve the identity's SSO organization before opening the transaction: the
	// Kratos lookup is a network call that must not run under the per-user lock.
	var ssoOrgID uuid.UUID
	if identity != nil {
		orgID, err := s.profiles.GetIdentityOrganizationID(ctx, identity.Subject)
		if err != nil {
			return ProvisionedTeam{}, fmt.Errorf("resolve sso organization: %w", err)
		}
		ssoOrgID = orgID
	}

	authTxDB, tx, err := s.authDB.WithTx(ctx)
	if err != nil {
		return ProvisionedTeam{}, fmt.Errorf("start transaction: %w", err)
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
			return ProvisionedTeam{}, fmt.Errorf("get user identity: %w", err)
		}
	}

	candidateUserID := profile.UserID
	if err := authTxDB.UpsertPublicUser(ctx, candidateUserID); err != nil {
		return ProvisionedTeam{}, fmt.Errorf("upsert public user: %w", err)
	}
	if identity != nil {
		canonicalUserID, err := authTxDB.UpsertPublicIdentity(ctx, authqueries.UpsertPublicIdentityParams{
			OidcIss: identity.Issuer,
			OidcSub: identity.Subject,
			UserID:  candidateUserID,
		})
		if err != nil {
			return ProvisionedTeam{}, fmt.Errorf("upsert public identity: %w", err)
		}
		if canonicalUserID != candidateUserID {
			// concurrent bootstrap claimed the identity first; drop the orphan candidate row
			if err := authTxDB.DeletePublicUser(ctx, candidateUserID); err != nil {
				return ProvisionedTeam{}, fmt.Errorf("delete orphan public user: %w", err)
			}
			profile.UserID = canonicalUserID
		}
	}

	// Serialize bootstrap for a user even when they have no team memberships yet.
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

		// Backfill the Ory identity's external_id only after the user/identity/team
		// are durably committed, so a failed PATCH never outlives a rolled-back
		// transaction. This is also the recovery path: a user whose external_id was
		// never set (e.g. a prior bootstrap whose PATCH failed after commit) re-runs
		// here and the PATCH is re-asserted. setOIDCIdentityExternalID is idempotent.
		if err := s.setOIDCIdentityExternalID(ctx, identity, profile.UserID); err != nil {
			return ProvisionedTeam{}, err
		}

		return ProvisionedTeam{
			ID:            existingTeam.ID,
			Name:          existingTeam.Name,
			Email:         existingTeam.Email,
			Slug:          existingTeam.Slug,
			IsBlocked:     existingTeam.IsBlocked,
			BlockedReason: existingTeam.BlockedReason,
		}, nil
	}
	if !dberrors.IsNotFoundError(err) {
		return ProvisionedTeam{}, fmt.Errorf("get default team: %w", err)
	}

	// Also reached by returning SSO members, who never have a default team.
	if ssoOrgID != uuid.Nil {
		landing, err := s.enrollSSOMember(ctx, authTxDB, profile.UserID, ssoOrgID)
		if err != nil {
			return ProvisionedTeam{}, err
		}

		if err := tx.Commit(ctx); err != nil {
			return ProvisionedTeam{}, fmt.Errorf("commit sso bootstrap transaction: %w", err)
		}

		// No billing: SSO teams are provisioned out of band.
		if err := s.setOIDCIdentityExternalID(ctx, identity, profile.UserID); err != nil {
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
	// fire-and-forget, but setOIDCIdentityExternalID returns early on failure, and
	// the existing-team recovery path only re-fires ProvisionTeam within
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

	// Backfill external_id only after the user/identity/team are durably committed.
	// A PATCH failure here is recoverable: external_id stays unset, the dashboard
	// re-runs bootstrap on the next login and re-asserts it via the existing-team
	// path above.
	if err := s.setOIDCIdentityExternalID(ctx, identity, profile.UserID); err != nil {
		return ProvisionedTeam{}, err
	}

	return ProvisionedTeam{
		ID:            team.ID,
		Name:          team.Name,
		Email:         team.Email,
		Slug:          team.Slug,
		IsBlocked:     team.IsBlocked,
		BlockedReason: team.BlockedReason,
	}, nil
}

// setOIDCIdentityExternalID stores the canonical public.users id on the Ory
// identity. It is a no-op for non-OIDC bootstrap (identity == nil).
func (s *Service) setOIDCIdentityExternalID(ctx context.Context, identity *bootstrapUserIdentity, userID uuid.UUID) error {
	if identity == nil {
		return nil
	}

	if err := s.profiles.SetIdentityExternalID(ctx, identity.Subject, userID); err != nil {
		return fmt.Errorf("set ory identity external id: %w", err)
	}

	return nil
}
