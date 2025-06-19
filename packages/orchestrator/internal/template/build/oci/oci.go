package oci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildah"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ToMBShift = 20

func GetImage(ctx context.Context, tracer trace.Tracer, artifactRegistry artifactsregistry.ArtifactsRegistry, templateId string, buildId string) (containerregistry.Image, error) {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	platform := containerregistry.Platform{
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

func GetImageSize(img containerregistry.Image) (int64, error) {
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

func ToExt4(ctx context.Context, tracer trace.Tracer, img containerregistry.Image, rootfsPath string, maxSize int64, blockSize int64) (int64, error) {
	err := ext4.Make(ctx, tracer, rootfsPath, maxSize>>ToMBShift, blockSize)
	if err != nil {
		return 0, fmt.Errorf("error creating ext4 file: %w", err)
	}

	err = ExtractToExt4(ctx, tracer, img, rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error extracting image to ext4 filesystem: %w", err)
	}

	// The filesystem is first created with the maximum size, so we need to shrink it to the actual size
	size, err := ext4.Shrink(ctx, tracer, rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error shrinking ext4 filesystem: %w", err)
	}

	ext4.LogMetadata(rootfsPath, zap.Int64("size", size))

	return size, nil
}

func ExtractToExt4(ctx context.Context, tracer trace.Tracer, img containerregistry.Image, rootfsPath string) error {
	tmpMount, err := os.MkdirTemp("", "ext4-mount")
	if err != nil {
		return fmt.Errorf("error creating temporary mount point: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(tmpMount); removeErr != nil {
			zap.L().Error("error removing temporary mount point", zap.Error(removeErr))
		}
	}()

	err = ext4.Mount(ctx, tracer, rootfsPath, tmpMount)
	if err != nil {
		return fmt.Errorf("error mounting ext4 filesystem: %w", err)
	}
	defer func() {
		if unmountErr := ext4.Unmount(ctx, tracer, tmpMount); unmountErr != nil {
			zap.L().Error("error unmounting ext4 filesystem", zap.Error(unmountErr))
		}
	}()

	zap.L().Debug("extracting image to ext4 filesystem",
		zap.String("rootfs_path", rootfsPath),
		zap.String("tmp_mount", tmpMount),
	)

	err = unpackRootfs(ctx, tracer, img, tmpMount)
	if err != nil {
		return fmt.Errorf("error extracting tar to directory: %w", err)
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

func unpackRootfs(ctx context.Context, tracer trace.Tracer, srcImage containerregistry.Image, destDir string) (err error) {
	_, childSpan := tracer.Start(ctx, "unpack-rootfs")
	defer childSpan.End()

	ociPath, err := os.MkdirTemp("", "oci-image")
	if err != nil {
		return fmt.Errorf("while creating temporary file for squashed image: %w", err)
	}
	defer func() {
		os.Remove(ociPath)
	}()

	tag := "latest"

	// Create an OCI layout in the temporary directory
	err = createOCIExport(ctx, tracer, srcImage, tag, ociPath)

	// Extract the rootfs from the OCI layout
	containerName, err := buildah.From(ctx, tracer, "oci:"+ociPath+":"+tag)
	if err != nil {
		return fmt.Errorf("while creating buildah container from OCI layout: %w", err)
	}
	defer func() {
		if removeErr := buildah.Remove(ctx, tracer, containerName); removeErr != nil {
			zap.L().Error("error removing buildah container", zap.Error(removeErr))
		}
	}()

	mountPath, err := buildah.Mount(ctx, tracer, containerName)
	if err != nil {
		return fmt.Errorf("while mounting buildah container: %w", err)
	}
	defer func() {
		if unmountErr := buildah.Unmount(ctx, tracer, containerName); unmountErr != nil {
			zap.L().Error("error unmounting buildah container", zap.Error(unmountErr))
		}
	}()

	err = copyFiles(ctx, tracer, mountPath, destDir)
	if err != nil {
		return fmt.Errorf("while copying files from buildah container mount point to destination directory: %w", err)
	}

	return nil
}

func copyFiles(ctx context.Context, tracer trace.Tracer, src, dest string) error {
	_, childSpan := tracer.Start(ctx, "copy-files")
	defer childSpan.End()

	cmd := exec.Command("rsync", "-a", "--whole-file", "--inplace", src+"/", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("while copying files from %s to %s: %w: %s", src, dest, err, string(out))
	}
	return nil
}

func createOCIExport(ctx context.Context, tracer trace.Tracer, srcImage containerregistry.Image, tag, path string) error {
	_, childSpan := tracer.Start(ctx, "create-oci-export")
	defer childSpan.End()

	p, err := layout.Write(path, empty.Index)
	if err != nil {
		return fmt.Errorf("while creating OCI layout: %w", err)
	}
	err = p.AppendImage(srcImage, layout.WithAnnotations(map[string]string{
		"org.opencontainers.image.ref.name": tag,
	}))
	if err != nil {
		return fmt.Errorf("while appending image to OCI layout: %w", err)
	}
	telemetry.ReportEvent(ctx, "created oci layout")

	return nil
}
