package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// key used where the actual secret is stored under the given path
const secretKey = "value"

type Client struct {
	client      *vault.Client
	logger      *zap.Logger
	renewTicker *time.Ticker
	stopRenew   chan struct{}
	roleID      string
	secretID    string
}

type ClientConfig struct {
	// Vault server address (e.g., "https://vault-leader.service.consul:8200")
	Address string
	// AppRole Role ID for authentication, see init-vault.sh
	RoleID string
	// AppRole Secret ID for authentication
	SecretID string
	// CA certificate to verify server (optional, PEM format)
	CACert string
	// Logger instance (optional)
	Logger *zap.Logger
}

func NewClient(ctx context.Context, config ClientConfig) (*Client, error) {
	// Set defaults
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}

	tlsConfig := vault.TLSConfiguration{
		InsecureSkipVerify: true,
	}

	if config.CACert != "" {
		tlsConfig.ServerCertificate = vault.ServerCertificateEntry{
			FromBytes: []byte(config.CACert),
		}
		tlsConfig.InsecureSkipVerify = false
	}

	vaultClient, err := vault.New(
		vault.WithAddress(config.Address),
		vault.WithRequestTimeout(30*time.Second),
		vault.WithTLS(tlsConfig),
		vault.WithRetryConfiguration(vault.RetryConfiguration{
			// slightly more aggressive than default with more retries
			RetryWaitMin: 50 * time.Millisecond,
			RetryWaitMax: 2 * time.Second,
			RetryMax:     10,
		}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create vault client")
	}

	client := &Client{
		client:    vaultClient,
		logger:    config.Logger,
		stopRenew: make(chan struct{}),
		roleID:    config.RoleID,
		secretID:  config.SecretID,
	}

	// Authenticate with AppRole
	leaseDuration, err := client.authenticate(ctx, config.RoleID, config.SecretID)
	if err != nil {
		return nil, errors.Wrap(err, "failed to authenticate with vault")
	}

	// Start token renewal once, based on the initial lease duration
	client.startTokenRenewal(ctx, leaseDuration)

	return client, nil
}

var (
	ErrVaultAddrNotSet     = errors.New("VAULT_ADDR environment variable is not set")
	ErrVaultRoleIDNotSet   = errors.New("VAULT_APPROLE_ROLE_ID environment variable is not set")
	ErrVaultSecretIDNotSet = errors.New("VAULT_APPROLE_SECRET_ID environment variable is not set")
)

func NewClientFromEnv(ctx context.Context) (*Client, error) {
	logger, _ := zap.NewProduction()

	config := ClientConfig{
		Address:  os.Getenv("VAULT_ADDR"),
		RoleID:   os.Getenv("VAULT_APPROLE_ROLE_ID"),
		SecretID: os.Getenv("VAULT_APPROLE_SECRET_ID"),
		CACert:   os.Getenv("VAULT_TLS_CA"),
		Logger:   logger,
	}

	if config.Address == "" {
		return nil, ErrVaultAddrNotSet
	}
	if config.RoleID == "" {
		return nil, ErrVaultRoleIDNotSet
	}
	if config.SecretID == "" {
		return nil, ErrVaultSecretIDNotSet
	}

	return NewClient(ctx, config)
}

var ErrAuthResponseMissing = errors.New("authentication response missing auth data")

// authenticate performs AppRole authentication and returns the lease duration
func (c *Client) authenticate(ctx context.Context, roleID, secretID string) (time.Duration, error) {
	resp, err := c.client.Auth.AppRoleLogin(ctx, schema.AppRoleLoginRequest{
		RoleId:   roleID,
		SecretId: secretID,
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to authenticate with vault")
	}

	if resp == nil || resp.Auth == nil {
		return 0, ErrAuthResponseMissing
	}

	if err := c.client.SetToken(resp.Auth.ClientToken); err != nil {
		return 0, errors.Wrap(err, "failed to set client token")
	}

	c.logger.Info("successfully authenticated with vault",
		zap.String("accessor", resp.Auth.Accessor),
		zap.Int("lease_duration", resp.Auth.LeaseDuration),
	)

	return time.Duration(resp.Auth.LeaseDuration) * time.Second, nil
}

// startTokenRenewal starts a background goroutine to renew the token
func (c *Client) startTokenRenewal(ctx context.Context, leaseDuration time.Duration) {
	if c.renewTicker != nil {
		return
	}
	// Renew at 1/3 of the lease duration
	renewInterval := max(leaseDuration/3, time.Minute)

	c.renewTicker = time.NewTicker(renewInterval)

	go func() {
		for {
			select {
			case <-ctx.Done():
				c.logger.Info("stopping token renewal due to context cancellation")
				return
			case <-c.stopRenew:
				c.logger.Info("stopping token renewal")
				return
			case <-c.renewTicker.C:
				// Use a context with timeout for each renewal attempt
				renewCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				c.renewToken(renewCtx)
				cancel()
			}
		}
	}()
}

var ErrTokenRenewalFailed = errors.New("failed to renew token")

// renewToken renews the current authentication token
func (c *Client) renewToken(ctx context.Context) {
	resp, err := c.client.Auth.TokenRenewSelf(ctx, schema.TokenRenewSelfRequest{})
	if err != nil {
		c.logger.Error("failed to renew token, attempting re-authentication", zap.Error(err))
		if _, authErr := c.authenticate(ctx, c.roleID, c.secretID); authErr != nil {
			c.logger.Error("failed to re-authenticate", zap.Error(authErr))
		}
		return
	}

	// if the token is not renewable, we need to re-authenticate
	if resp.Auth != nil && !resp.Auth.Renewable {
		c.logger.Info("token is not renewable, attempting re-authentication")
		if _, authErr := c.authenticate(ctx, c.roleID, c.secretID); authErr != nil {
			c.logger.Error("failed to re-authenticate", zap.Error(authErr))
		}
		return
	}

	if resp != nil && resp.Auth != nil {
		c.logger.Debug("token renewed",
			zap.Time("renewed_at", time.Now()),
			zap.Int("lease_duration", resp.Auth.LeaseDuration),
		)
	}
}

var ErrSecretNotFound = errors.New("secret not found")

// GetSecret retrieves a secret and its unseralized metadata from Vault at the specified path
func (c *Client) GetSecret(ctx context.Context, path string) (string, map[string]any, error) {
	resp, err := c.client.Secrets.KvV2Read(ctx, path)
	if err != nil && !vault.IsErrorStatus(err, http.StatusNotFound) {
		return "", nil, errors.Wrap(err, "failed to read secret")
	}

	if resp == nil || resp.Data.Data == nil || vault.IsErrorStatus(err, http.StatusNotFound) {
		return "", nil, ErrSecretNotFound
	}

	value, ok := resp.Data.Data[secretKey].(string)
	if !ok {
		return "", nil, ErrSecretNotFound
	}

	c.logger.Debug("secret retrieved",
		zap.String("path", path),
	)

	return value, resp.Data.Metadata, nil
}

// WriteSecret writes a secret to Vault at the specified path, metadata will be serialized as key=json
func (c *Client) WriteSecret(ctx context.Context, path string, value string, metadata map[string]any) error {
	_, err := c.client.Secrets.KvV2Write(ctx, path, schema.KvV2WriteRequest{
		Data: map[string]any{
			secretKey: value,
		},
	})
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to write secret at path %s", path))
	}

	c.logger.Debug("secret written",
		zap.String("path", path),
		zap.Int("metadata_keys", len(metadata)),
	)

	// metadata must be key:value pairs and value must be string, so make it json
	serializedMetadata := make(map[string]any, len(metadata))
	for key, value := range metadata {
		valueJsonStr, err := json.Marshal(value)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("failed to marshal metadata value for key %s", key))
		}
		serializedMetadata[key] = string(valueJsonStr)
	}

	if _, err := c.client.Secrets.KvV2WriteMetadata(ctx, path, schema.KvV2WriteMetadataRequest{
		CasRequired:        false,
		CustomMetadata:     serializedMetadata,
		DeleteVersionAfter: time.Duration(0).String(),
		MaxVersions:        1,
	}); err != nil {
		// clean up the secret if metadata write fails
		_, err := c.client.Secrets.KvV2Delete(ctx, path)
		if err != nil {
			c.logger.Error("failed to clean up secret", zap.Error(err))
		}

		return errors.Wrap(err, fmt.Sprintf("failed to write metadata at path %s", path))
	}

	return nil
}

// DeleteSecret deletes a secret and all its versions from Vault at the specified path
func (c *Client) DeleteSecret(ctx context.Context, path string) error {
	// Delete all versions of the secret
	_, err := c.client.Secrets.KvV2DeleteMetadataAndAllVersions(ctx, path)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to delete secret at path %s", path))
	}

	c.logger.Debug("secret deleted", zap.String("path", path))
	return nil
}

// Close stops token renewal and cleans up resources
func (c *Client) Close() {
	close(c.stopRenew)

	if c.renewTicker != nil {
		c.renewTicker.Stop()
		c.renewTicker = nil
	}
}
