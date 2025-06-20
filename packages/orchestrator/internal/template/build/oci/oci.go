package oci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containers/storage/pkg/archive"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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
	defer func() {
		os.Remove(ociPath)
	}()

	// Create export of layers in the temporary directory
	layers, err := createExport(ctx, tracer, postProcessor, srcImage, ociPath)
	if err != nil {
		return fmt.Errorf("while creating export of source image: %w", err)
	}

	// Mount the overlay filesystem with the extracted layers
	mountPath, err := os.MkdirTemp("", "overlayfs-mount")
	if err != nil {
		return fmt.Errorf("while creating temporary file for squashed image: %w", err)
	}
	defer os.RemoveAll(mountPath)

	err = ext4.MountOverlayFS(ctx, tracer, layers, mountPath)
	if err != nil {
		return fmt.Errorf("while mounting overlayfs with layers: %w", err)
	}
	defer func() {
		if unmountErr := ext4.Unmount(ctx, tracer, mountPath); unmountErr != nil {
			zap.L().Error("error unmounting overlayfs mount point", zap.Error(unmountErr))
		}
	}()

	// List files in the mount point
	files, err := listFiles(ctx, tracer, mountPath)
	if err != nil {
		return fmt.Errorf("while listing files in overlayfs: %w", err)
	}
	postProcessor.WriteMsg("Root filesystem structure:")
	postProcessor.WriteMsg(strings.Join(files, ", "))

	// Copy files from the overlayfs mount point to the destination directory
	err = copyFiles(ctx, tracer, mountPath, destDir)
	if err != nil {
		return fmt.Errorf("while copying files from overlayfs to destination directory: %w", err)
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

	return utils.Map(files, func(file os.DirEntry) string {
		return file.Name()
	}), nil
}

// copyFiles uses rsync to copy files from the source directory to the destination directory.
func copyFiles(ctx context.Context, tracer trace.Tracer, src, dest string) error {
	_, childSpan := tracer.Start(ctx, "copy-files")
	defer childSpan.End()

	cmd := exec.Command("rsync", "-a", "--whole-file", "--inplace", src+"/", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("while copying files from %s to %s: %w: %s", src, dest, err, string(out))
	}
	return nil
}

// createExport extracts the layers of the source image into a temporary directory
// and returns the paths of the extracted layers. The layers are extracted in reverse order
// to maintain the correct order for overlayFS.
// The layers are extracted in parallel to speed up the process.
func createExport(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, srcImage containerregistry.Image, path string) ([]string, error) {
	ctx, childSpan := tracer.Start(ctx, "create-oci-export")
	defer childSpan.End()

	layers, err := srcImage.Layers()
	if err != nil {
		return nil, fmt.Errorf("while getting layers of source image: %w", err)
	}

	layerPaths := make([]string, len(layers))
	var eg errgroup.Group
	// Need to iterate in reverse order to maintain the correct layer order for overlayfs
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		digest, err := l.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest of layer %d: %w", i, err)
		}
		telemetry.ReportEvent(ctx, "uncompressing layer", attribute.Int("layer.index", i), attribute.String("layer.digest", digest.String()))
		postProcessor.WriteMsg(fmt.Sprintf("Uncompressing layer: %s", digest))

		// Each layer has to be uniquely named, even if the digest is the same across different layers
		layerPath := filepath.Join(path, fmt.Sprintf("layer-%d-%s", i, strings.ReplaceAll(digest.String(), ":", "-")))
		layerPaths[i] = layerPath
		eg.Go(func() error {
			err := os.MkdirAll(layerPath, 0o755)
			if err != nil {
				return fmt.Errorf("failed to create directory for layer %d: %w", i, err)
			}

			rc, err := l.Uncompressed()
			if err != nil {
				return fmt.Errorf("failed to get uncompressed layer %d: %w", i, err)
			}
			defer rc.Close()

			err = archive.Untar(rc, layerPath, &archive.TarOptions{})
			if err != nil {
				return fmt.Errorf("failed to untar layer %d: %w", i, err)
			}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("while extracting layers: %w", err)
	}

	postProcessor.WriteMsg("Layers extracted")

	return layerPaths, nil
}
