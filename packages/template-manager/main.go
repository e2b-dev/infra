package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logging"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	"github.com/e2b-dev/infra/packages/template-manager/internal/server"
	"github.com/e2b-dev/infra/packages/template-manager/internal/test"
)

const defaultPort = 5009

var commitSHA string

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testFlag := flag.String("test", "", "run tests")
	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")

	port := flag.Int("port", defaultPort, "Port for test HTTP server")

	log.Println("Starting template manager", "commit", commitSHA)

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
