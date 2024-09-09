package test

import (
	"context"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	templateStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"

	"cloud.google.com/go/storage"
)

func Run(envID, buildID, instanceID string, keepAlive, count *int) {
	ctx := context.Background()

	// Start of mock build for testing
	dns := dns.New()
	go dns.Start("127.0.0.4:53")

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		errMsg := fmt.Errorf("failed to create GCS client: %v", err)
		panic(errMsg)
	}

	templateCache := templateStorage.NewTemplateDataCache(ctx, client, constants.BucketName)

	sandbox.MockInstance(envID, buildID, instanceID, dns, templateCache, time.Duration(*keepAlive)*time.Second)
	sandbox.MockInstance(envID, buildID, "is-2", dns, templateCache, time.Duration(*keepAlive)*time.Second)
}
