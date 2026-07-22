package auth

import (
	"context"
	"net/http"

	"github.com/redis/go-redis/v9"

	internalauthservice "github.com/e2b-dev/infra/packages/auth/internal/service"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
)

type Service = internalauthservice.Service

type authService = internalauthservice.AuthService

func NewAuthService(
	ctx context.Context,
	redisClient redis.UniversalClient,
	authDB *authdb.Client,
	providerConfig ProviderConfig,
	httpClient *http.Client,
) (*authService, error) {
	return internalauthservice.NewAuthService(ctx, redisClient, authDB, providerConfig, httpClient)
}
