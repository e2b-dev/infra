package orchestrator

import (
	"context"
	"errors"
	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	nomadapi "github.com/hashicorp/nomad/api"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Orchestrator struct {
	nomadClient     *nomadapi.Client
	instanceCache   *instance.InstanceCache
	nodes           map[string]*Node
	tracer          trace.Tracer
	logger          *zap.SugaredLogger
	analytics       *analyticscollector.Analytics
	instanceCounter *metric.Int64UpDownCounter
}

func New(ctx context.Context, tracer trace.Tracer, nomadClient *nomadapi.Client, logger *zap.SugaredLogger, instanceCounter *metric.Int64UpDownCounter, posthogClient *analyticscollector.PosthogClient) (*Orchestrator, error) {
	analytics, err := analyticscollector.NewAnalytics()
	if err != nil {
		logger.Errorf("Error initializing Analytics client\n: %v", err)
	}

	o := Orchestrator{
		analytics:       analytics,
		instanceCounter: instanceCounter,
		nomadClient:     nomadClient,
		logger:          logger,
		tracer:          tracer,
	}

	cache := instance.NewCache(analytics.Client, logger,
		o.getInsertInstanceFunction(ctx, logger),
		o.getDeleteInstanceFunction(ctx, posthogClient, logger), *instanceCounter)

	o.instanceCache = cache

	return &o, nil
}

func (o *Orchestrator) Close() error {
	var err error
	for _, node := range o.nodes {
		closeErr := node.Client.Close()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}

	closeErr := o.analytics.Close()
	if closeErr != nil {
		err = errors.Join(err, closeErr)
	}

	return err
}
