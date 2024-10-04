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

func Run(templateId, buildId, sandboxId string, keepAlive, count *int) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(*keepAlive)+time.Second*20)
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
		MockSandbox(ctx, templateId, buildId, sandboxId+"-"+strconv.Itoa(i), dns, templateCache, time.Duration(*keepAlive)*time.Second)
	}
}
