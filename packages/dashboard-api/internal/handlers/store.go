package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/cfg"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var _ api.ServerInterface = (*APIStore)(nil)

type APIStore struct {
	config     cfg.Config
	db         *sqlcdb.Client
	clickhouse clickhouse.Clickhouse
}

func NewAPIStore(config cfg.Config, db *sqlcdb.Client, ch clickhouse.Clickhouse) *APIStore {
	return &APIStore{
		config:     config,
		db:         db,
		clickhouse: ch,
	}
}

func (s *APIStore) GetHealth(c *gin.Context) {
	c.String(http.StatusOK, "Health check successful")
}

type supabaseClaims struct {
	jwt.RegisteredClaims
}

func (s *APIStore) GetUserIDFromSupabaseToken(ctx context.Context, _ *gin.Context, supabaseToken string) (uuid.UUID, *api.APIError) {
	claims, err := getJWTClaims(ctx, s.config.SupabaseJWTSecrets, supabaseToken)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       err,
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	userId, err := claims.GetSubject()
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed getting jwt subject: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	userIDParsed, err := uuid.Parse(userId)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed parsing user uuid: %w", err),
			ClientMsg: "Backend authentication failed",
			Code:      http.StatusUnauthorized,
		}
	}

	return userIDParsed, nil
}

func (s *APIStore) ValidateTeamID(_ context.Context, _ *gin.Context, teamID string) (uuid.UUID, *api.APIError) {
	parsed, err := uuid.Parse(teamID)
	if err != nil {
		return uuid.UUID{}, &api.APIError{
			Err:       fmt.Errorf("failed parsing team uuid: %w", err),
			ClientMsg: "Invalid team ID",
			Code:      http.StatusBadRequest,
		}
	}

	return parsed, nil
}

func getJWTClaims(ctx context.Context, secrets []string, token string) (*supabaseClaims, error) {
	errs := make([]error, 0)

	for _, secret := range secrets {
		if len(secret) < cfg.MinSupabaseJWTSecretLength {
			logger.L().Warn(ctx, "jwt secret is too short and will be ignored",
				zap.Int("min_length", cfg.MinSupabaseJWTSecretLength),
				zap.String("secret_start", secret[:min(3, len(secret))]))

			continue
		}

		token, err := jwt.ParseWithClaims(token, &supabaseClaims{}, func(token *jwt.Token) (any, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}

			return []byte(secret), nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to parse supabase token: %w", err))

			continue
		}

		if claims, ok := token.Claims.(*supabaseClaims); ok && token.Valid {
			return claims, nil
		}
	}

	if len(errs) == 0 {
		return nil, errors.New("failed to parse supabase token, no secrets found")
	}

	return nil, errors.Join(errs...)
}
