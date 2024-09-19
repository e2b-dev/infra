package mock

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	sandboxStorage "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/storage"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"

	"cloud.google.com/go/storage"
)

func Run(envID, buildID, instanceID string, keepAlive, count *int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(*keepAlive)+time.Second*60)
	defer cancel()

	// Start of mock build for testing
	dns := dns.New()
	go dns.Start("127.0.0.4:53")

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		errMsg := fmt.Errorf("failed to create GCS client: %v", err)
		panic(errMsg)
	}

	templateCache := sandboxStorage.NewTemplateDataCache(ctx, client, templateStorage.BucketName)

	for i := 0; i < *count; i++ {
		MockInstance(ctx, envID, buildID, instanceID+"-"+strconv.Itoa(i), dns, templateCache, time.Duration(*keepAlive)*time.Second)
	}
}
