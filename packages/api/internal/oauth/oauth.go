package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
)

type Verifier interface {
	VerifyClaims(ctx context.Context, rawToken string) (Claims, error)
}

type Claims struct {
	Subject string
	OrgID   string
}

type tokenClaims struct {
	OrgID string `json:"org_id"`
}

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
}

type noopVerifier struct{}

func NewVerifier(ctx context.Context, issuerURL string, audience string) (Verifier, error) {
	issuerURL = strings.TrimSpace(issuerURL)
	audience = strings.TrimSpace(audience)

	if issuerURL == "" && audience == "" {
		return noopVerifier{}, nil
	}
	if issuerURL == "" || audience == "" {
		return nil, errors.New("client proxy OIDC issuer URL and audience must both be configured")
	}

	provider, err := oidc.NewProvider(ctx, issuerURL)
	if err != nil {
		return nil, fmt.Errorf("create client proxy OIDC provider: %w", err)
	}

	return &oidcVerifier{
		verifier: provider.Verifier(&oidc.Config{ClientID: audience}),
	}, nil
}

func (noopVerifier) VerifyClaims(context.Context, string) (Claims, error) {
	return Claims{}, errors.New("client proxy OIDC verifier is not configured")
}

func (v *oidcVerifier) VerifyClaims(ctx context.Context, rawToken string) (Claims, error) {
	idToken, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Claims{}, fmt.Errorf("verify client proxy OIDC token: %w", err)
	}

	claims := tokenClaims{}
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, fmt.Errorf("parse client proxy OIDC claims: %w", err)
	}

	return Claims{
		Subject: idToken.Subject,
		OrgID:   claims.OrgID,
	}, nil
}

func BearerToken(md metadata.MD) (string, bool) {
	vals := md.Get(proxygrpc.MetadataAuthorization)
	if len(vals) == 0 {
		return "", false
	}

	scheme, token, ok := strings.Cut(vals[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}

	return strings.TrimSpace(token), true
}

func RequireBearer(md metadata.MD) error {
	if _, found := BearerToken(md); !found {
		return denyPermission()
	}

	return nil
}

func RequireClaims(ctx context.Context, md metadata.MD, verifier Verifier) (Claims, error) {
	if verifier == nil {
		return Claims{}, status.Error(codes.Internal, "client proxy OIDC verifier is not configured")
	}

	rawToken, found := BearerToken(md)
	if !found {
		return Claims{}, denyPermission()
	}

	claims, err := verifier.VerifyClaims(ctx, rawToken)
	if err != nil {
		return Claims{}, denyPermission()
	}

	return claims, nil
}

func RequireOrgClaims(claims Claims, expectedOrgID string) error {
	expectedOrgID = strings.TrimSpace(expectedOrgID)
	if expectedOrgID == "" {
		return denyPermission()
	}
	if subtle.ConstantTimeCompare([]byte(claims.OrgID), []byte(expectedOrgID)) != 1 {
		return denyPermission()
	}

	return nil
}

func RequireOrg(ctx context.Context, md metadata.MD, verifier Verifier, expectedOrgID string) error {
	claims, err := RequireClaims(ctx, md, verifier)
	if err != nil {
		return err
	}

	return RequireOrgClaims(claims, expectedOrgID)
}

func denyPermission() error {
	return status.Error(codes.PermissionDenied, "permission denied")
}
