package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/mock"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultPort = 5008

func main() {
	templateId := flag.String("template", "", "template id")
	buildId := flag.String("build", "", "build id")
	sandboxId := flag.String("sandbox", "", "sandbox id")
	keepAlive := flag.Int("alive", 0, "keep alive")
	count := flag.Int("count", 1, "number of serially spawned sandboxes")

	port := flag.Int("port", defaultPort, "Port for test HTTP server")

	flag.Parse()

	// If we're running a test, we don't need to start the server
	if *templateId != "" && *buildId != "" && *sandboxId != "" {
		mock.Run(*templateId, *buildId, *sandboxId, keepAlive, count)

		return
	}

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(server.ServiceName, "no")
		defer shutdown()
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		log.Fatalf("Error initializing logging\n: %v\n", err)
	}
	// Create an instance of our handler which satisfies the generated interface
	s := server.New(logger.Desugar())

	log.Printf("Starting server on port %d", *port)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
