package factories

import (
	"context"
	"fmt"
	"net"

	"github.com/soheilhy/cmux"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func NewCMUXServer(ctx context.Context, port uint16, meterProvider metric.MeterProvider) (cmux.CMux, error) {
	var lisCfg net.ListenConfig
	lis, err := lisCfg.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	m := cmux.New(lis)

	meter := meterProvider.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/factories")
	errCounter := utils.Must(telemetry.GetCounter(meter, telemetry.CmuxErrorsTotal))

	m.HandleError(func(err error) bool {
		logger.L().Warn(ctx, "cmux connection error", zap.Error(err))
		errCounter.Add(ctx, 1)

		return true // keep serving
	})

	return m, nil
}
