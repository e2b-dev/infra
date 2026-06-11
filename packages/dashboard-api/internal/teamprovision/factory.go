package teamprovision

import (
	"context"
	"errors"
	"strings"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	ErrMissingBaseURL  = errors.New("billing server url is required when billing server api token is configured")
	ErrMissingAPIToken = errors.New("billing server api token is required when billing server url is configured")
)

func NewProvisionSink(ctx context.Context, baseURL, apiToken string) (TeamProvisionSink, error) {
	baseURL = strings.TrimSpace(baseURL)
	apiToken = strings.TrimSpace(apiToken)

	if baseURL == "" && apiToken == "" {
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

	return NewHTTPProvisionSink(baseURL, apiToken), nil
}
