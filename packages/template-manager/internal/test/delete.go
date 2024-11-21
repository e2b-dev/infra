package test

import (
	"context"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

func Delete(templateID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	tracer := otel.Tracer("test")

	artifactRegistry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	templateStorage := template.NewTemplateStorage(ctx)

	err = template.Delete(ctx, tracer, artifactRegistry, templateStorage, templateID)
	if err != nil {
		panic(err)
	}
}
