package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/vault"
)

var ErrSecretNotFound = errors.New("secret not found")

// CreateSecret creates a new secret in both the database and vault
func CreateSecret(
	ctx context.Context,
	db *sqlcdb.Client,
	vaultClient vault.VaultBackend,
	teamID uuid.UUID,
	value string,
	label string,
	description string,
	allowlist []string,
) (*queries.CreateSecretRow, error) {
	secretID := uuid.New()

	// Store secret value in vault
	vaultPath := getVaultPath(teamID, secretID)
	if err := vaultClient.WriteSecret(ctx, vaultPath, value, map[string]any{
		"team_id":   teamID.String(),
		"secret_id": secretID.String(),
		"label":     label,
	}); err != nil {
		return nil, fmt.Errorf("failed to store secret in vault: %w", err)
	}

	// Store secret metadata in database
	secret, err := db.CreateSecret(ctx, queries.CreateSecretParams{
		ID:          secretID,
		TeamID:      teamID,
		Label:       label,
		Description: description,
		Allowlist:   allowlist,
	})
	if err != nil {
		// Attempt to clean up vault entry if database insert fails
		if deleteErr := vaultClient.DeleteSecret(ctx, vaultPath); deleteErr != nil {
			zap.L().Error("failed to clean up vault secret after database error",
				zap.Error(deleteErr),
				zap.String("vault_path", vaultPath),
			)
		}
		return nil, fmt.Errorf("failed to store secret in database: %w", err)
	}

	return &secret, nil
}

// DeleteSecret deletes a secret from both the database and vault
func DeleteSecret(
	ctx context.Context,
	db *sqlcdb.Client,
	vaultClient vault.VaultBackend,
	teamID uuid.UUID,
	secretID uuid.UUID,
) error {
	// Delete from database first to ensure it's actually the team's secret
	rowsAffected, err := db.DeleteSecret(ctx, queries.DeleteSecretParams{
		ID:     secretID,
		TeamID: teamID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete secret from database: %w", err)
	}

	// Check if the secret existed
	if rowsAffected == 0 {
		return ErrSecretNotFound
	}

	// Delete from vault
	vaultPath := getVaultPath(teamID, secretID)
	if err := vaultClient.DeleteSecret(ctx, vaultPath); err != nil {
		// Log but don't fail if vault deletion fails
		// The database record is already gone, which is the source of truth
		zap.L().Warn("failed to delete secret from vault",
			zap.Error(err),
			zap.String("vault_path", vaultPath),
			zap.String("secret_id", secretID.String()),
		)
	}

	return nil
}

// UpdateSecret updates a secret's label and description in the database
func UpdateSecret(
	ctx context.Context,
	db *sqlcdb.Client,
	teamID uuid.UUID,
	secretID uuid.UUID,
	label string,
	description string,
) error {
	// Update in database
	rowsAffected, err := db.UpdateSecret(ctx, queries.UpdateSecretParams{
		ID:          secretID,
		TeamID:      teamID,
		Label:       label,
		Description: description,
	})
	if err != nil {
		return fmt.Errorf("failed to update secret in database: %w", err)
	}

	// Check if the secret existed and belonged to the team
	if rowsAffected == 0 {
		return ErrSecretNotFound
	}

	return nil
}

// getVaultPath returns the vault path for a team's secret
func getVaultPath(teamID uuid.UUID, secretID uuid.UUID) string {
	return fmt.Sprintf("teams/%s/secrets/%s", teamID.String(), secretID.String())
}
