package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const defaultPort = 5008

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := flag.Int("port", defaultPort, "orchestrator server port")

	flag.Parse()

	if !env.IsLocal() {
		shutdown := telemetry.InitOTLPExporter(ctx, server.ServiceName, "no")
		defer shutdown(context.TODO())
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s, err := server.New()
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	log.Printf("starting server on port %d", *port)

	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
