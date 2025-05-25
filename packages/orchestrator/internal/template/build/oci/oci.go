package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ToMBShift = 20

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

func GetImage(ctx context.Context, tracer trace.Tracer, dockerTag string) (v1.Image, error) {
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

func GetImageSize(img v1.Image) (int64, error) {
	imageSize := int64(0)

	layers, err := img.Layers()
	if err != nil {
		return 0, fmt.Errorf("error getting image layers: %w", err)
	}

	for index, layer := range layers {
		layerSize, err := layer.Size()
		if err != nil {
			return 0, fmt.Errorf("error getting layer (%d) size: %w", index, err)
		}
		imageSize += layerSize
	}

	return imageSize, nil
}

func ToExt4(ctx context.Context, img v1.Image, rootfsPath string, sizeLimit int64) error {
	r := mutate.Extract(img)
	defer r.Close()

	rootfsFile, err := os.Create(rootfsPath)
	if err != nil {
		return fmt.Errorf("error creating rootfs file: %w", err)
	}
	defer func() {
		rootfsErr := rootfsFile.Close()
		if rootfsErr != nil {
			telemetry.ReportError(ctx, fmt.Errorf("error closing rootfs file: %w", rootfsErr))
		} else {
			telemetry.ReportEvent(ctx, "closed rootfs file")
		}
	}()

	// Convert tar to ext4 image
	if err := tar2ext4.Convert(r, rootfsFile, tar2ext4.ConvertWhiteout, tar2ext4.MaximumDiskSize(sizeLimit)); err != nil {
		if strings.Contains(err.Error(), "disk exceeded maximum size") {
			return fmt.Errorf("build failed - exceeded maximum size %v MB", sizeLimit>>ToMBShift)
		}
		return fmt.Errorf("error converting tar to ext4: %w", err)
	}

	// Sync the metadata and data to disk.
	// This is important to ensure that the file is fully written when used by other processes, like FC.
	if err := rootfsFile.Sync(); err != nil {
		return fmt.Errorf("error syncing rootfs file: %w", err)
	}

	return nil
}
