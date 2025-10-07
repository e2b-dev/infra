package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/secrets"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// MaxSecretValueBytes is 8KB to match typical HTTP header value limits and keep memory cache reasonable.
	// Vault supports at least up to 512KB: https://developer.hashicorp.com/vault/docs/internals/limits
	MaxSecretValueBytes = 8192
	// MaxSecretLabelChars limits label length for readability and reasonable storage
	MaxSecretLabelChars = 256
	// MaxSecretDescriptionChars limits description length for readability and reasonable storage
	MaxSecretDescriptionChars = 1024
	// MaxSecretAllowlistHosts is an arbitrary but reasonable limit
	MaxSecretAllowlistHosts = 10
)

func (a *APIStore) GetSecrets(c *gin.Context) {
	ctx := c.Request.Context()

	teamID := a.GetTeamInfo(c).Team.ID

	secretsFeatureFlag, err := a.featureFlags.BoolFlag(ctx, featureflags.SecretsFeatureFlag)
	if err != nil {
		zap.L().Error("Failed to get Secrets feature flag", zap.Error(err))
	}

	if !secretsFeatureFlag {
		a.sendAPIStoreError(c, http.StatusForbidden, "Secrets feature flag is disabled")
		return
	}

	secretsDB, err := a.sqlcDB.GetTeamSecrets(ctx, teamID)
	if err != nil {
		zap.L().Warn("error when getting team secrets", zap.Error(err), logger.WithTeamID(teamID.String()))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting team secrets")

		telemetry.ReportCriticalError(ctx, "error when getting team secrets", err)

		return
	}

	secretsList := make([]api.Secret, len(secretsDB))
	for i, secret := range secretsDB {
		secretsList[i] = api.Secret{
			Id:          secret.ID,
			Label:       secret.Label,
			Description: &secret.Description,
			Allowlist:   secret.Allowlist,
			CreatedAt:   secret.CreatedAt,
		}
	}

	zap.L().Debug("Fetched team secrets",
		logger.WithTeamID(teamID.String()),
		zap.Int("secrets_count", len(secretsList)),
	)

	c.JSON(http.StatusOK, secretsList)
}

func (a *APIStore) PostSecrets(c *gin.Context) {
	ctx := c.Request.Context()

	teamID := a.GetTeamInfo(c).Team.ID

	secretsFeatureFlag, err := a.featureFlags.BoolFlag(ctx, featureflags.SecretsFeatureFlag)
	if err != nil {
		zap.L().Error("Failed to get Secrets feature flag", zap.Error(err))
	}

	if !secretsFeatureFlag {
		a.sendAPIStoreError(c, http.StatusForbidden, "Secrets feature flag is disabled")
		return
	}

	body, err := utils.ParseBody[api.NewSecret](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Value == "" {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Secret value cannot be empty")
		return
	}

	if len(body.Value) > MaxSecretValueBytes {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Secret value cannot exceed %d bytes (got %d bytes)", MaxSecretValueBytes, len(body.Value)))
		return
	}

	if body.Label == "" {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Label cannot be empty")
		return
	}

	if len(body.Label) > MaxSecretLabelChars {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Label cannot exceed %d characters (got %d)", MaxSecretLabelChars, len(body.Label)))
		return
	}

	if len(body.Description) > MaxSecretDescriptionChars {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Description cannot exceed %d characters (got %d)", MaxSecretDescriptionChars, len(body.Description)))
		return
	}

	if len(body.Allowlist) > MaxSecretAllowlistHosts {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Too many hosts in allowlist (%d), only %d allowed", len(body.Allowlist), MaxSecretAllowlistHosts))
		return
	}

	// default value, should be done by the SDK/Dashboard/CLI but just in case
	if len(body.Allowlist) == 0 {
		body.Allowlist = []string{"*"}
	}

	for _, host := range body.Allowlist {
		if err := validateHostname(host); err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	secret, err := secrets.CreateSecret(ctx, a.sqlcDB, a.secretVault, teamID, body.Value, body.Label, body.Description, body.Allowlist)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when creating team secret: %s", err))

		telemetry.ReportCriticalError(ctx, "error when creating team secret", err)

		return
	}

	telemetry.ReportEvent(ctx, "Created secret")

	isWildcardAllowlist := len(secret.Allowlist) == 1 && secret.Allowlist[0] == "*"
	zap.L().Debug("Created secret",
		logger.WithTeamID(teamID.String()),
		zap.String("secret_id", secret.ID.String()),
		zap.Int("label_length", len(secret.Label)),
		zap.Int("allowlist_size", len(secret.Allowlist)),
		zap.Bool("wildcard_allowlist", isWildcardAllowlist),
		zap.Int("value_bytes", len(body.Value)),
	)

	c.JSON(http.StatusCreated, api.CreatedSecret{
		Id:          secret.ID,
		Label:       secret.Label,
		Description: secret.Description,

		Allowlist: secret.Allowlist,
		CreatedAt: secret.CreatedAt,
	})
}

func (a *APIStore) PatchSecretsSecretID(c *gin.Context, secretID string) {
	ctx := c.Request.Context()
	teamID := a.GetTeamInfo(c).Team.ID

	secretsFeatureFlag, err := a.featureFlags.BoolFlag(ctx, featureflags.SecretsFeatureFlag)
	if err != nil {
		zap.L().Error("Failed to get Secrets feature flag", zap.Error(err))
	}

	if !secretsFeatureFlag {
		a.sendAPIStoreError(c, http.StatusForbidden, "Secrets feature flag is disabled")
		return
	}

	secretIDParsed, err := uuid.Parse(secretID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing secret ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing secret ID", err)
		return
	}

	body, err := utils.ParseBody[api.UpdateSecret](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	if body.Label == "" {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Label cannot be empty")
		return
	}

	if len(body.Label) > MaxSecretLabelChars {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Label cannot exceed %d characters (got %d)", MaxSecretLabelChars, len(body.Label)))
		return
	}

	if len(body.Description) > MaxSecretDescriptionChars {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Description cannot exceed %d characters (got %d)", MaxSecretDescriptionChars, len(body.Description)))
		return
	}

	if err := secrets.UpdateSecret(ctx, a.sqlcDB, teamID, secretIDParsed, body.Label, body.Description); err != nil {
		if errors.Is(err, secrets.ErrSecretNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, "Secret not found")
			return
		}
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating secret: %s", err))

		telemetry.ReportCriticalError(ctx, "error when updating secret", err)
		return
	}

	zap.L().Debug("Updated secret",
		logger.WithTeamID(teamID.String()),
		zap.String("secret_id", secretID),
		zap.Int("label_length", len(body.Label)),
		zap.Int("description_length", len(body.Description)),
	)

	c.Status(http.StatusOK)
}

func (a *APIStore) DeleteSecretsSecretID(c *gin.Context, secretID string) {
	ctx := c.Request.Context()
	teamID := a.GetTeamInfo(c).Team.ID

	secretsFeatureFlag, err := a.featureFlags.BoolFlag(ctx, featureflags.SecretsFeatureFlag)
	if err != nil {
		zap.L().Error("Failed to get Secrets feature flag", zap.Error(err))
	}

	if !secretsFeatureFlag {
		a.sendAPIStoreError(c, http.StatusForbidden, "Secrets feature flag is disabled")
		return
	}

	secretIDParsed, err := uuid.Parse(secretID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing secret ID: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing secret ID", err)
		return
	}

	if err := secrets.DeleteSecret(ctx, a.sqlcDB, a.secretVault, teamID, secretIDParsed); err != nil {
		if errors.Is(err, secrets.ErrSecretNotFound) {
			a.sendAPIStoreError(c, http.StatusNotFound, "Secret not found")
			return
		}
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting secret: %s", err))

		telemetry.ReportCriticalError(ctx, "error when deleting secret", err)
		return
	}

	zap.L().Debug("Deleted secret",
		logger.WithTeamID(teamID.String()),
		zap.String("secret_id", secretID),
	)

	c.Status(http.StatusNoContent)
}

// ValidateHostname validates a hostname with wildcard support
// Allowed: example.com, *.example.com, something.*.example.com, *, *.*
// Not allowed: URLs with schemes, paths, or invalid characters
// See the test cases for more examples.
func validateHostname(hostname string) error {
	// Most will be a wildcard anyway so we can skip the rest of the checks
	if hostname == "*" {
		return nil
	}

	// Check for common URL indicators that make it invalid
	if strings.Contains(hostname, "://") ||
		strings.Contains(hostname, "/") ||
		strings.HasPrefix(hostname, "http") {
		return fmt.Errorf("invalid hostname pattern: %s, cannot contain schemes (https/http) or paths (/api/, /v2/)", hostname)
	}

	// Check if its a valid go glob pattern
	// match continues scanning to the end of the pattern even after a mismatch, so by matching "" we can check if the host is a valid pattern
	// https://go-review.googlesource.com/c/go/+/264397
	if _, err := filepath.Match(hostname, ""); err != nil {
		return fmt.Errorf("invalid hostname pattern: %w", err)
	}

	// Regex pattern for hostname validation with wildcards
	// - Each label can be alphanumeric with hyphens (not starting/ending with hyphen)
	// - OR it can be a wildcard (*)
	// - Labels are separated by dots
	pattern := `^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?|\*)(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?|\.\*)*$`

	if matched, err := regexp.MatchString(pattern, hostname); err != nil || !matched {
		return fmt.Errorf("invalid hostname pattern: %s", hostname)
	}

	return nil
}
