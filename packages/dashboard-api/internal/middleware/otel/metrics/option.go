package metrics

import (
	"net/http"

	"go.opentelemetry.io/otel/attribute"
)

type Option interface {
	apply(cfg *config)
}

type optionFunc func(cfg *config)

func (fn optionFunc) apply(cfg *config) {
	fn(cfg)
}

func WithAttributes(attributes func(serverName, route string, request *http.Request) []attribute.KeyValue) Option {
	return optionFunc(func(cfg *config) {
		cfg.attributes = attributes
	})
}

func WithRecordInFlightDisabled() Option {
	return optionFunc(func(cfg *config) {
		cfg.recordInFlight = false
	})
}

func WithRecordDurationDisabled() Option {
	return optionFunc(func(cfg *config) {
		cfg.recordDuration = false
	})
}

func WithRecordSizeDisabled() Option {
	return optionFunc(func(cfg *config) {
		cfg.recordSize = false
	})
}

func WithRecorder(recorder Recorder) Option {
	return optionFunc(func(cfg *config) {
		cfg.recorder = recorder
	})
}

func WithShouldRecordFunc(shouldRecord func(serverName, route string, request *http.Request) bool) Option {
	return optionFunc(func(cfg *config) {
		cfg.shouldRecord = shouldRecord
	})
}
