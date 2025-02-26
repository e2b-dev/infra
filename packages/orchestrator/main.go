package main

import (
	"context"
	"flag"
	"fmt"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.uber.org/zap"
)

const defaultPort = 5008

var logsCollectorAddress = env.GetEnv("LOGS_COLLECTOR_ADDRESS", "")

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := flag.Int("port", defaultPort, "orchestrator server port")

	flag.Parse()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, server.ServiceName, "no")
		defer shutdown(context.TODO())
	}

	logger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:      server.ServiceName,
		IsInternal:       true,
		IsDevelopment:    env.IsLocal(),
		IsDebug:          env.IsDebug(),
		CollectorAddress: logsCollectorAddress,
	}))
	defer logger.Sync()

	zap.ReplaceGlobals(logger)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		zap.L().Fatal("failed to listen", zap.Error(err))
	}

	s, err := server.New()
	if err != nil {
		zap.L().Fatal("failed to create server", zap.Error(err))
	}

	logger.Info("Starting orchestrator server", zap.Int("port", *port))

	if err := s.Serve(lis); err != nil {
		zap.L().Fatal("failed to serve", zap.Error(err))
	}
}
