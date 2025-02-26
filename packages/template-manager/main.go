package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	"github.com/e2b-dev/infra/packages/template-manager/internal/server"
	"github.com/e2b-dev/infra/packages/template-manager/internal/test"
	"go.uber.org/zap"
)

const (
	defaultPort = 5009
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testFlag := flag.String("test", "", "run tests")
	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")

	port := flag.Int("port", defaultPort, "Port for test HTTP server")

	flag.Parse()

	if err := constants.CheckRequired(); err != nil {
		log.Fatalf("Validation for environment variables failed: %v", err)
	}

	// If we're running a test, we don't need to start the server
	if *testFlag != "" {
		switch *testFlag {
		case "build":
			test.Build(*templateID, *buildID)
			return
		}
	}

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, constants.ServiceName, "no")
		defer shutdown(context.TODO())
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	logger := zap.Must(logger.NewLogger(ctx, logger.LoggerConfig{
		ServiceName:      constants.ServiceName,
		IsInternal:       true,
		IsDevelopment:    env.IsLocal(),
		IsDebug:          true,
		CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
	}))
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	// Create an instance of our handler which satisfies the generated interface
	s := server.New(logger)

	log.Printf("Starting server on port %d", *port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
