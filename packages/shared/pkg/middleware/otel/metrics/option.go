package metrics

import (
	"net/http"
)

// Option applies a configuration to the given config
type Option interface {
	apply(cfg *config)
}

type optionFunc func(cfg *config)

func (fn optionFunc) apply(cfg *config) {
	fn(cfg)
}

// WithShouldRecordFunc sets a func using which whether a record should be recorded
// By default the all api calls are recorded
func WithShouldRecordFunc(shouldRecord func(serverName, route string, request *http.Request) bool) Option {
	return optionFunc(func(cfg *config) {
		cfg.shouldRecord = shouldRecord
	})
}
