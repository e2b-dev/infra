package teamprovision

import (
	"errors"
	"time"
)

var ErrMissingAPIToken = errors.New("billing server api token is required when billing server url is configured")

func NewProvisionSink(baseURL, apiToken string, timeout time.Duration) (TeamProvisionSink, error) {
	if baseURL == "" {
		return NewNoopProvisionSink(), nil
	}

	if apiToken == "" {
		return nil, ErrMissingAPIToken
	}

	return NewHTTPProvisionSink(baseURL, apiToken, timeout), nil
}
