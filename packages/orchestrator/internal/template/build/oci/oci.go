package oci

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildah"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ToMBShift            = 20
	tarballExportUpdates = 10
)

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

func ToExt4(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, img containerregistry.Image, rootfsPath string, maxSize int64, blockSize int64) (int64, error) {
	err := ext4.Make(ctx, tracer, rootfsPath, maxSize>>ToMBShift, blockSize)
	if err != nil {
		return 0, fmt.Errorf("error creating ext4 file: %w", err)
	}

	err = ExtractToExt4(ctx, tracer, postProcessor, img, rootfsPath)
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

func ExtractToExt4(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, img containerregistry.Image, rootfsPath string) error {
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

	err = unpackRootfs(ctx, tracer, postProcessor, img, tmpMount)
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

func unpackRootfs(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, srcImage containerregistry.Image, destDir string) (err error) {
	ctx, childSpan := tracer.Start(ctx, "unpack-rootfs")
	defer childSpan.End()

	ociPath, err := os.MkdirTemp("", "oci-image")
	if err != nil {
		return fmt.Errorf("while creating temporary file for squashed image: %w", err)
	}
	//defer func() {
	//	os.Remove(ociPath)
	//}()

	tag := uuid.New().String()

	// Create export in the temporary directory
	prefix, imageName, err := createExport(ctx, tracer, postProcessor, srcImage, tag, ociPath)
	if err != nil {
		return fmt.Errorf("while creating export of source image: %w", err)
	}
	telemetry.ReportEvent(ctx, "created export", attribute.String("prefix", prefix), attribute.String("image.name", imageName))

	// Extract the rootfs from the image path
	containerName, err := buildah.From(ctx, tracer, fmt.Sprintf("%s:%s:%s", prefix, imageName, tag))
	if err != nil {
		return fmt.Errorf("while creating buildah container from OCI layout: %w", err)
	}
	defer func() {
		// Will remove the container as well as the mount point
		if removeErr := buildah.Remove(ctx, tracer, containerName); removeErr != nil {
			zap.L().Error("error removing buildah container", zap.Error(removeErr))
		}

		go func() {
			// Removing the image will remove the container as well as the mount point (if not already removed)
			if removeErr := buildah.RemoveImage(ctx, tracer, imageName); removeErr != nil {
				zap.L().Error("error removing buildah image", zap.Error(removeErr))
			}
		}()
	}()

	mountPath, err := buildah.Mount(ctx, tracer, containerName)
	if err != nil {
		return fmt.Errorf("while mounting buildah container: %w", err)
	}

	// List files in the mount point
	files, err := listFiles(ctx, tracer, mountPath)
	if err != nil {
		return fmt.Errorf("while listing files in buildah container mount point: %w", err)
	}
	postProcessor.WriteMsg("Filesystem root structure:")
	for _, file := range files {
		postProcessor.WriteMsg(fmt.Sprintf("  %s", file))
	}

	err = copyFiles(ctx, tracer, mountPath, destDir)
	if err != nil {
		return fmt.Errorf("while copying files from buildah container mount point to destination directory: %w", err)
	}

	return nil
}

func listFiles(ctx context.Context, tracer trace.Tracer, dir string) ([]string, error) {
	_, childSpan := tracer.Start(ctx, "list-files")
	defer childSpan.End()

	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("while reading directory %s: %w", dir, err)
	}
	var fileList []string
	for _, file := range files {
		fileList = append(fileList, file.Name())
	}

	return fileList, nil
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

func createExport(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, srcImage containerregistry.Image, tag, path string) (string, string, error) {
	ctx, childSpan := tracer.Start(ctx, "create-oci-export")
	defer childSpan.End()

	mediaType, err := srcImage.MediaType()
	if err != nil {
		return "", "", fmt.Errorf("while getting media type of source image: %w", err)
	}
	switch mediaType {
	case types.OCIManifestSchema1:
		// OCI export layout
		p, err := layout.Write(path, empty.Index)
		if err != nil {
			return "", "", fmt.Errorf("while creating OCI layout: %w", err)
		}
		err = p.AppendImage(srcImage, layout.WithAnnotations(map[string]string{
			"org.opencontainers.image.ref.name": tag,
		}))
		if err != nil {
			return "", "", fmt.Errorf("while appending image to OCI layout: %w", err)
		}
		telemetry.ReportEvent(ctx, "created oci layout")

		return "oci", path, nil
	case types.DockerManifestSchema2:
		// Docker tarball export
		filePath := filepath.Join(path, "docker-image.tar")

		ref, err := name.ParseReference("docker-archive")
		if err != nil {
			return "", "", fmt.Errorf("while parsing image reference: %w", err)
		}
		progress := make(chan containerregistry.Update, 200)
		go func() {
			defer close(progress)

			err := tarball.WriteToFile(filePath, ref, srcImage, tarball.WithProgress(progress))
			if err != nil {
				progress <- containerregistry.Update{Error: fmt.Errorf("while writing Docker image tarball: %w", err)}
				return
			}
		}()

		nextReport := int64(0)
	progressLoop:
		for update := range progress {
			switch {
			case update.Error != nil && errors.Is(update.Error, io.EOF):
				break progressLoop
			case update.Error != nil:
				return "", "", fmt.Errorf("error exporting Docker image: %w", update.Error)
			default:
				if nextReport <= update.Complete || update.Complete == update.Total {
					nextReport = update.Complete + (update.Total / tarballExportUpdates)
					telemetry.ReportEvent(ctx, "docker tarball export progress",
						attribute.Int64("complete", update.Complete),
						attribute.Int64("total", update.Total),
					)
					postProcessor.WriteMsg(fmt.Sprintf("Exporting Docker image: %s / %s", humanize.Bytes(uint64(update.Complete)), humanize.Bytes(uint64(update.Total))))
				}
			}
		}
		postProcessor.WriteMsg("Docker image exported")
		telemetry.ReportEvent(ctx, "created docker tarball")

		return "docker-archive", filePath, nil
	default:
		return "", "", fmt.Errorf("source image is not an OCI image index or Docker manifest, got: %s", mediaType)
	}
}
