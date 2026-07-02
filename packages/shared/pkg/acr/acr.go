// Package acr implements Azure Container Registry (ACR) authentication shared
// by the artifacts-registry and dockerhub backends. ACR does not accept AAD
// access tokens directly for docker operations; they must first be exchanged
// for an ACR refresh token via the registry's /oauth2/exchange endpoint.
package acr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/go-containerregistry/pkg/authn"
)

const (
	// tokenUsername is the well-known username ACR expects when the password
	// is an ACR refresh token.
	tokenUsername = "00000000-0000-0000-0000-000000000000"

	// aadScope is the AAD scope of the ACR audience used when requesting the
	// access token that is exchanged for an ACR refresh token.
	aadScope = "https://containerregistry.azure.net/.default"

	exchangeTimeout = 10 * time.Second
)

// Authenticator implements authn.Authenticator for Azure Container Registry
// by exchanging an AAD access token for an ACR refresh token.
type Authenticator struct {
	loginServer string
	credential  azcore.TokenCredential
	httpClient  *http.Client
}

var _ authn.Authenticator = (*Authenticator)(nil)

// NewAuthenticator creates an ACR authenticator for the given login server
// (e.g. myregistry.azurecr.io) backed by the given AAD credential.
func NewAuthenticator(loginServer string, credential azcore.TokenCredential) *Authenticator {
	return &Authenticator{
		loginServer: loginServer,
		credential:  credential,
		httpClient:  http.DefaultClient,
	}
}

// Authorization returns docker credentials for the registry. The AAD access
// token is exchanged for an ACR refresh token; ACR accepts the refresh token
// as the password for the well-known token username.
func (a *Authenticator) Authorization() (*authn.AuthConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), exchangeTimeout)
	defer cancel()

	token, err := a.credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{aadScope}})
	if err != nil {
		return nil, fmt.Errorf("failed to get AAD access token: %w", err)
	}

	refreshToken, err := a.exchangeToken(ctx, token.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange AAD token for ACR refresh token: %w", err)
	}

	return &authn.AuthConfig{
		Username: tokenUsername,
		Password: refreshToken,
	}, nil
}

func (a *Authenticator) exchangeToken(ctx context.Context, accessToken string) (string, error) {
	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {a.loginServer},
		"access_token": {accessToken},
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://%s/oauth2/exchange", a.loginServer),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create acr token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call acr token exchange endpoint: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

		return "", fmt.Errorf("acr token exchange failed with status %d: %s", resp.StatusCode, body)
	}

	var payload struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("failed to decode acr token exchange response: %w", err)
	}

	if payload.RefreshToken == "" {
		return "", errors.New("acr token exchange response did not contain a refresh token")
	}

	return payload.RefreshToken, nil
}
