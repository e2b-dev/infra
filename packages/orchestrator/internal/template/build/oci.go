package build

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var authConfig = authn.Basic{
	Username: "_json_key_base64",
	Password: consts.GoogleServiceAccountSecret,
}

func getRepositoryAuth() (authn.Authenticator, error) {
	authCfg := consts.DockerAuthConfig
	if authCfg == "" {
		return &authConfig, nil
	}

	decoded, err := base64.URLEncoding.DecodeString(authCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode auth config: %w", err)
	}

	var cfg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON auth config: %w", err)
	}

	return &authn.Basic{
		Username: cfg.Username,
		Password: cfg.Password,
	}, nil
}

func getOCIImage(ctx context.Context, tracer trace.Tracer, dockerTag string) (v1.Image, error) {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	auth, err := getRepositoryAuth()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	ref, err := name.ParseReference(dockerTag)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	platform := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	telemetry.ReportEvent(childCtx, "pulled image")
	return img, nil
}
