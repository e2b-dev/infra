package utils

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/utils")

func (wp *WarmPool[T]) startTimer(operation operationType, opts ...startOpt) *timer {
	t := &timer{
		histogram: wp.operationsMetric,
		start:     time.Now(),
		attrs: []attribute.KeyValue{
			attribute.String("operation", string(operation)),
		},
	}

	for _, opt := range opts {
		opt.applyStart(t)
	}

	return t
}

type timer struct {
	histogram metric.Int64Histogram
	start     time.Time

	attrs []attribute.KeyValue
}

type successOpt interface {
	applySuccess(t *timer)
}

func (t *timer) success(opts ...successOpt) {
	t.attrs = append(t.attrs, resultAttr(resultSuccess))

	for _, opt := range opts {
		opt.applySuccess(t)
	}
}

type failureOpt interface {
	applyFailure(t *timer)
}

func (t *timer) failure(opts ...failureOpt) {
	t.attrs = append(t.attrs, resultAttr(resultFailure))

	for _, opt := range opts {
		opt.applyFailure(t)
	}
}

func (t *timer) stop(ctx context.Context) {
	duration := time.Since(t.start).Milliseconds()

	go t.histogram.Record(ctx, duration, metric.WithAttributes(t.attrs...))
}

type resultType string

const (
	resultSuccess resultType = "success"
	resultFailure resultType = "failure"
)

func resultAttr(result resultType) attribute.KeyValue {
	return attribute.String("result", string(result))
}

type sourceType string

func (s sourceType) applySuccess(t *timer) {
	t.attrs = append(t.attrs, attribute.String("source", string(s)))
}

const (
	sourceFresh sourceType = "fresh"
	sourceReuse sourceType = "reuse"
)

type failureReasonType string

func (f failureReasonType) applyFailure(t *timer) {
	t.attrs = append(t.attrs, failureReasonAttr(f))
}

const (
	reasonPoolClosed    failureReasonType = "pool closed"
	reasonContextDone   failureReasonType = "canceled"
	reasonReturnTimeout failureReasonType = "return timeout"

	reasonReusableClosed failureReasonType = "reusable closed"
	reasonFreshClosed    failureReasonType = "fresh closed"

	reasonCleanupFresh    failureReasonType = "cleanup fresh"
	reasonCleanupReusable failureReasonType = "cleanup reusable"
)

func failureReasonAttr(reason failureReasonType) attribute.KeyValue {
	return attribute.String("reason", string(reason))
}

type operationType string

const (
	operationCreate   operationType = "create"
	operationDestroy  operationType = "destroy"
	operationClose    operationType = "close"
	operationGet      operationType = "get"
	operationPopulate operationType = "populate"
	operationReturn   operationType = "return"
)

type startOpt interface {
	applyStart(t *timer)
}

type withAttrOpt struct {
	key   string
	value string
}

func (o withAttrOpt) applyStart(t *timer) {
	t.attrs = append(t.attrs, attribute.String(o.key, o.value))
}

func withAttr[T ~string](key string, value T) withAttrOpt {
	return withAttrOpt{key: key, value: string(value)}
}
