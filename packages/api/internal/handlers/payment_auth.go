package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

// PaymentAuthenticator implements auth.Authenticator for the PaymentAuth
// security scheme. It allows requests with Authorization: Payment credentials
// to pass through the OpenAPI validation layer. The actual payment verification
// is handled by the MPP middleware.
type PaymentAuthenticator struct {
	enabled bool
	teamID  string
	getTeam func(ctx context.Context, teamID uuid.UUID) (*types.Team, error)
}

// NewPaymentAuthenticator creates a PaymentAuthenticator.
// When a valid Payment credential is present, it maps the request to the
// pre-configured MPP team so downstream handlers have a team context.
func NewPaymentAuthenticator(
	enabled bool,
	teamID string,
	getTeam func(ctx context.Context, teamID uuid.UUID) (*types.Team, error),
) auth.Authenticator {
	return &PaymentAuthenticator{
		enabled: enabled,
		teamID:  teamID,
		getTeam: getTeam,
	}
}

func (a *PaymentAuthenticator) SecuritySchemeName() string {
	return "PaymentAuth"
}

func (a *PaymentAuthenticator) Authenticate(ctx context.Context, ginCtx *gin.Context, input *openapi3filter.AuthenticationInput) error {
	if !a.enabled {
		return fmt.Errorf("payment auth is not enabled")
	}

	header := input.RequestValidationInput.Request.Header.Get("Authorization")
	if header == "" || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(header)), "payment ") {
		ginCtx.Status(http.StatusPaymentRequired)
		return &auth.AuthorizationHeaderMissingError{}
	}

	teamUUID, err := uuid.Parse(a.teamID)
	if err != nil {
		ginCtx.Status(http.StatusInternalServerError)
		return fmt.Errorf("invalid MPP team ID configuration: %w", err)
	}

	team, err := a.getTeam(ctx, teamUUID)
	if err != nil {
		ginCtx.Status(http.StatusInternalServerError)
		return fmt.Errorf("failed to get MPP team: %w", err)
	}

	auth.SetTeamInfo(ginCtx, team)

	return nil
}
