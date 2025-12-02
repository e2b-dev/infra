package ioc

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/fx"

	clickhouse "github.com/e2b-dev/infra/packages/clickhouse/pkg"
	"github.com/e2b-dev/infra/packages/clickhouse/pkg/events"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	flags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func newClickhouseModule(config cfg.Config) fx.Option {
	return If(
		"clickhouse",
		config.ClickhouseConnectionString != "",
		fx.Provide(
			newClickhouseDriver,
			AsDeliveryTarget(NewClickhouseDeliveryTarget),
		),
	).Build()
}

func newClickhouseDriver(config cfg.Config) (driver.Conn, error) {
	return clickhouse.NewDriver(config.ClickhouseConnectionString)
}

func NewClickhouseDeliveryTarget(ctx context.Context, conn driver.Conn, featureFlags *flags.Client) (*events.ClickhouseDelivery, error) {
	return events.NewDefaultClickhouseSandboxEventsDelivery(ctx, conn, featureFlags)
}
