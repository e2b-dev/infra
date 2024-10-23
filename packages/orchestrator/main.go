package main

import (
	"flag"
	"fmt"
	"net"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultPort = 5008

func main() {
	port := flag.Int("port", defaultPort, "Port for test HTTP server")

	flag.Parse()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(server.ServiceName, "no")
		defer shutdown()
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[orchestrator]: failed to listen: %v\n", err)

		return
	}

	logger, err := logging.New(env.IsLocal())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[orchestrator]: error initializing logging\n: %v\n", err)

		return
	}

	// Create an instance of our handler which satisfies the generated interface
	s := server.New(logger.Desugar())

	fmt.Fprintf(os.Stdout, "[orchestrator]: starting server on port %d\n", *port)

	if serverErr := s.Serve(lis); serverErr != nil {
		fmt.Fprintf(os.Stderr, "[orchestrator]: failed to serve: %v\n", serverErr)

		return
	}
}
