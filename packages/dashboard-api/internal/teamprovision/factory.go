package teamprovision

import (
	"context"
	"errors"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"go.uber.org/zap"
)

var (
	ErrMissingBaseURL  = errors.New("billing server url is required when billing http team provision sink is enabled")
	ErrMissingAPIToken = errors.New("billing server api token is required when billing http team provision sink is enabled")
)

func NewProvisionSink(enabled bool, baseURL, apiToken string, timeout time.Duration) (TeamProvisionSink, error) {
	if !enabled {
		logger.L().Info(context.Background(), "team provision sink configured",
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

	logger.L().Info(context.Background(), "team provision sink configured",
		zap.String("sink", "http"),
		zap.String("result", "enabled"),
		zap.String("base_url", baseURL),
	)

	return NewHTTPProvisionSink(baseURL, apiToken, timeout), nil
}
