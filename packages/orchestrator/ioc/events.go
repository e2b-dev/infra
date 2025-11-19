package ioc

import (
	"context"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	sharedevents "github.com/e2b-dev/infra/packages/shared/pkg/events"
)

const deliveryTargetGroupTag = `group:"delivery-targets"`

func AsDeliveryTarget(f any) any {
	return fx.Annotate(
		f,
		fx.As((*sharedevents.Delivery[sharedevents.SandboxEvent])(nil)),
		fx.ResultTags(deliveryTargetGroupTag),
	)
}

func withDeliveryTargets(f any) any {
	return fx.Annotate(
		f,
		fx.ParamTags(deliveryTargetGroupTag),
	)
}

func newSandboxEventsService(deliveryTargets []sharedevents.Delivery[sharedevents.SandboxEvent], lc fx.Lifecycle) *events.EventsService {
	svc := events.NewEventsService(deliveryTargets)

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return svc.Close(ctx)
		},
	})

	return svc
}
