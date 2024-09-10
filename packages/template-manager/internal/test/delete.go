package test

import (
	"context"
	"fmt"
	"time"

	templateShared "github.com/e2b-dev/infra/packages/shared/pkg/template"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/storage"
	"go.opentelemetry.io/otel"
)

func Delete(templateID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	tracer := otel.Tracer("test")

	artifactRegistry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	client, err := storage.NewClient(ctx, storage.WithJSONReads())
	if err != nil {
		errMsg := fmt.Errorf("failed to create GCS client: %v", err)
		panic(errMsg)
	}

	templateStorage := template.NewTemplateStorage(ctx, client, templateShared.BucketName)

	err = template.Delete(ctx, tracer, artifactRegistry, templateStorage, templateID)
	if err != nil {
		panic(err)
	}
}
