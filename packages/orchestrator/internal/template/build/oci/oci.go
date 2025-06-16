package oci

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"go.opentelemetry.io/otel/trace"

	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ToMBShift = 20

func GetImage(ctx context.Context, tracer trace.Tracer, artifactRegistry artifactsregistry.ArtifactsRegistry, templateId string, buildId string) (v1.Image, error) {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	platform := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	img, err := artifactRegistry.GetImage(childCtx, templateId, buildId, platform)
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
			telemetry.ReportError(ctx, "error closing rootfs file", rootfsErr)
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

func ParseEnvs(envs []string) map[string]string {
	envMap := make(map[string]string, len(envs))
	for _, env := range envs {
		if strings.TrimSpace(env) == "" {
			continue
		}
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key != "" && value != "" {
			envMap[key] = value
		}
	}
	return envMap
}
