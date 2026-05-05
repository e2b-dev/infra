package teamprovision

import (
	"context"
	"errors"

	"go.uber.org/zap"

	supabasedb "github.com/e2b-dev/infra/packages/db/pkg/supabase"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	ErrMissingBaseURL  = errors.New("billing server url is required when billing http team provision sink is enabled")
	ErrMissingAPIToken = errors.New("billing server api token is required when billing http team provision sink is enabled")
)

func NewProvisionSink(ctx context.Context, enabled bool, baseURL, apiToken string, supabaseDB *supabasedb.Client) (TeamProvisionSink, error) {
	if !enabled {
		logger.L().Info(ctx, "team provision sink configured",
			zap.String("sink", "noop"),
			zap.String("result", "disabled"),
		)

		return NewNoopProvisionSink(), nil
	}

	if baseURL == "" {
		return nil, ErrMissingBaseURL
	}

	if apiToken == "" {
		return nil, ErrMissingAPIToken
	}

	logger.L().Info(ctx, "team provision sink configured",
		zap.String("sink", "http"),
		zap.String("result", "enabled"),
		zap.String("base_url", baseURL),
	)

	return NewHTTPProvisionSink(baseURL, apiToken, supabaseDB), nil
}
