package ioc

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/events"
	sharedevents "github.com/e2b-dev/infra/packages/shared/pkg/events"
	"go.uber.org/fx"
)

const deliveryTargetGroupTag = `group:"delivery-targets"`

func AsDeliveryTarget(f any) any {
	return fx.Annotate(
		f,
		fx.As(new(sharedevents.Delivery[sharedevents.SandboxEvent])),
		fx.ResultTags(deliveryTargetGroupTag),
	)
}

func WithDeliveryTargets(f any) any {
	return fx.Annotate(
		f,
		fx.ParamTags(deliveryTargetGroupTag),
	)
}

func NewSandboxEventsService(deliveryTargets []sharedevents.Delivery[sharedevents.SandboxEvent]) *events.EventsService {
	return events.NewEventsService(deliveryTargets)
}
