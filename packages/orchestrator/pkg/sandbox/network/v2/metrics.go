//go:build linux

package v2

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	metricCreationFailures           = "orchestrator.network.v2.slots_pool.creation_failures"
	metricMutationFailures           = "orchestrator.network.v2.host_firewall.mutation_failures"
	metricReconciliationFailures     = "orchestrator.network.v2.host_firewall.reconciliation_failures"
	metricConnectionResets           = "orchestrator.network.v2.host_firewall.connection_resets"
	metricConnectionResetFailures    = "orchestrator.network.v2.host_firewall.connection_reset_failures"
	metricReconciliationSkipped      = "orchestrator.network.v2.host_firewall.reconciliation_skipped"
	operationAddSlot                 = "add_slot"
	operationRemoveSlot              = "remove_slot"
	operationReconcile               = "reconcile"
	reconciliationSkipUncleanReclaim = "unclean_startup_reclaim"
)

// Metrics contains the new, v2-specific operational metrics. It is safe to
// share one instance between the v2 pool and host firewall.
type Metrics struct {
	creationFailures        metric.Int64Counter
	mutationFailures        metric.Int64Counter
	reconciliationFailures  metric.Int64Counter
	connectionResets        metric.Int64Counter
	connectionResetFailures metric.Int64Counter
	reconciliationSkipped   metric.Int64Counter
}

func NewMetrics(meterProvider metric.MeterProvider) (*Metrics, error) {
	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network/v2")
	creationFailures, err := meter.Int64Counter(metricCreationFailures, metric.WithDescription("V2 network slot creation failures."), metric.WithUnit("{failure}"))
	if err != nil {
		return nil, err
	}
	mutationFailures, err := meter.Int64Counter(metricMutationFailures, metric.WithDescription("V2 host firewall slot mutation failures."), metric.WithUnit("{failure}"))
	if err != nil {
		return nil, err
	}
	reconciliationFailures, err := meter.Int64Counter(metricReconciliationFailures, metric.WithDescription("V2 host firewall reconciliation failures."), metric.WithUnit("{failure}"))
	if err != nil {
		return nil, err
	}
	connectionResets, err := meter.Int64Counter(metricConnectionResets, metric.WithDescription("V2 host firewall nftables connection reset attempts."), metric.WithUnit("{reset}"))
	if err != nil {
		return nil, err
	}
	connectionResetFailures, err := meter.Int64Counter(metricConnectionResetFailures, metric.WithDescription("V2 host firewall nftables connection reset failures."), metric.WithUnit("{failure}"))
	if err != nil {
		return nil, err
	}
	reconciliationSkipped, err := meter.Int64Counter(metricReconciliationSkipped, metric.WithDescription("V2 host firewall reconciliations skipped for safety."), metric.WithUnit("{skip}"))
	if err != nil {
		return nil, err
	}

	return &Metrics{
		creationFailures:        creationFailures,
		mutationFailures:        mutationFailures,
		reconciliationFailures:  reconciliationFailures,
		connectionResets:        connectionResets,
		connectionResetFailures: connectionResetFailures,
		reconciliationSkipped:   reconciliationSkipped,
	}, nil
}

func (m *Metrics) recordCreationFailure(ctx context.Context, stage slotCreationStage) {
	m.creationFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("stage", string(stage))))
}

func (m *Metrics) recordHostFirewallFailure(ctx context.Context, operation string, reconciliation bool, reset func() error) error {
	operationAttrs := metric.WithAttributes(attribute.String("operation", operation))
	if reconciliation {
		m.reconciliationFailures.Add(ctx, 1)
	} else {
		m.mutationFailures.Add(ctx, 1, operationAttrs)
	}

	m.connectionResets.Add(ctx, 1, operationAttrs)
	resetErr := reset()
	if resetErr != nil {
		m.connectionResetFailures.Add(ctx, 1, operationAttrs)
	}

	return resetErr
}

func (m *Metrics) RecordReconciliationSkipped(ctx context.Context) {
	m.reconciliationSkipped.Add(ctx, 1, metric.WithAttributes(
		attribute.String("reason", reconciliationSkipUncleanReclaim),
	))
}

func joinOperationError(operationErr *error, resetErr error) {
	*operationErr = errors.Join(*operationErr, resetErr)
}
