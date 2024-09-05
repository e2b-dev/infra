package test

import (
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

func Run(envID, buildID, instanceID string, keepAlive, count *int) {
	// Start of mock build for testing
	dns := dns.New()
	go dns.Start("127.0.0.4:53")

	sandbox.MockInstance(envID, buildID, instanceID, dns, time.Duration(*keepAlive)*time.Second)
}
