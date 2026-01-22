package oci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containers/storage/pkg/archive"
	"github.com/dustin/go-humanize"
	"github.com/google/go-containerregistry/pkg/name"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci/auth"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci")

const (
	ToMBShift            = 20
	tarballExportUpdates = 10
)

var DefaultPlatform = containerregistry.Platform{
	OS:           "linux",
	Architecture: "amd64",
}

// wrapImagePullError converts technical Docker registry errors into user-friendly messages.
func wrapImagePullError(err error, imageRef string) error {
	if err == nil {
		return nil
	}

	// Check for transport errors with specific error codes from the registry API
	var transportErr *transport.Error
	if errors.As(err, &transportErr) {
		for _, e := range transportErr.Errors {
			switch e.Code {
			case transport.ManifestUnknownErrorCode:
				return fmt.Errorf("image '%s' not found: the image or tag does not exist in the registry", imageRef)
			case transport.NameUnknownErrorCode:
				return fmt.Errorf("repository '%s' not found: verify the image name is correct", imageRef)
			case transport.UnauthorizedErrorCode:
				return fmt.Errorf("access denied to '%s': authentication required or insufficient permissions", imageRef)
			case transport.DeniedErrorCode:
				return fmt.Errorf("access denied to '%s': you don't have permission to pull this image", imageRef)
			}
		}
	}

	return fmt.Errorf("failed to pull image '%s': %w", imageRef, err)
}

func GetPublicImage(ctx context.Context, dockerhubRepository dockerhub.RemoteRepository, tag string, authProvider auth.RegistryAuthProvider) (containerregistry.Image, error) {
	ctx, span := tracer.Start(ctx, "pull-public-docker-image")
	defer span.End()

	ref, err := name.ParseReference(tag)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference '%s': %w", tag, err)
	}

	platform := DefaultPlatform

	// When no auth provider is provided and the image is from the default registry
	// use docker remote repository proxy with cached images
	if authProvider == nil && ref.Context().RegistryStr() == name.DefaultRegistry {
		img, err := dockerhubRepository.GetImage(ctx, tag, platform)
		if err != nil {
			return nil, wrapImagePullError(err, tag)
		}

		telemetry.ReportEvent(ctx, "pulled public image through docker remote repository proxy")

		err = verifyImagePlatform(img, platform)
		if err != nil {
			return nil, err
		}

		return img, nil
	}

	// Build options
	opts := []remote.Option{remote.WithPlatform(platform)}

	// Use the auth provider if provided
	if authProvider != nil {
		authOption, err := authProvider.GetAuthOption(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting auth option: %w", err)
		}
		if authOption != nil {
			opts = append(opts, authOption)
		}
	}

	img, err := remote.Image(ref, opts...)
	if err != nil {
		return nil, wrapImagePullError(err, tag)
	}

	telemetry.ReportEvent(ctx, "pulled public image")

	err = verifyImagePlatform(img, platform)
	if err != nil {
		return nil, err
	}

	return img, nil
}

func GetImage(ctx context.Context, artifactRegistry artifactsregistry.ArtifactsRegistry, templateId string, buildId string) (containerregistry.Image, error) {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	platform := DefaultPlatform

	img, err := artifactRegistry.GetImage(childCtx, templateId, buildId, platform)
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	telemetry.ReportEvent(childCtx, "pulled image")

	err = verifyImagePlatform(img, platform)
	if err != nil {
		return nil, err
	}

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

func ToExt4(ctx context.Context, logger logger.Logger, img containerregistry.Image, rootfsPath string, maxSize int64, blockSize int64) (int64, error) {
	ctx, childSpan := tracer.Start(ctx, "oci-to-ext4")
	defer childSpan.End()

	err := filesystem.Make(ctx, rootfsPath, maxSize>>ToMBShift, blockSize)
	if err != nil {
		return 0, fmt.Errorf("error creating ext4 file: %w", err)
	}

	err = ExtractToExt4(ctx, logger, img, rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error extracting image to ext4 filesystem: %w", err)
	}

	// Check the FS integrity first so no errors occur during shrinking
	_, err = filesystem.CheckIntegrity(ctx, rootfsPath, true)
	if err != nil {
		return 0, fmt.Errorf("error checking filesystem integrity after ext4 creation: %w", err)
	}

	// The filesystem is first created with the maximum size, so we need to shrink it to the actual size
	size, err := filesystem.Shrink(ctx, rootfsPath)
	if err != nil {
		return 0, fmt.Errorf("error shrinking ext4 filesystem: %w", err)
	}

	// Check the FS integrity after shrinking
	_, err = filesystem.CheckIntegrity(ctx, rootfsPath, true)
	if err != nil {
		return 0, fmt.Errorf("error checking filesystem integrity after shrinking: %w", err)
	}

	return size, nil
}

func ExtractToExt4(ctx context.Context, l logger.Logger, img containerregistry.Image, rootfsPath string) error {
	ctx, childSpan := tracer.Start(ctx, "extract-to-ext4")
	defer childSpan.End()

	tmpMount, err := os.MkdirTemp("", "ext4-mount")
	if err != nil {
		return fmt.Errorf("error creating temporary mount point: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(tmpMount); removeErr != nil {
			logger.L().Error(ctx, "error removing temporary mount point", zap.Error(removeErr))
		}
	}()

	err = filesystem.Mount(ctx, rootfsPath, tmpMount)
	if err != nil {
		return fmt.Errorf("error mounting ext4 filesystem: %w", err)
	}
	defer func() {
		if unmountErr := filesystem.Unmount(context.WithoutCancel(ctx), tmpMount); unmountErr != nil {
			logger.L().Error(ctx, "error unmounting ext4 filesystem", zap.Error(unmountErr))
		}
	}()

	logger.L().Debug(ctx, "extracting image to ext4 filesystem",
		zap.String("rootfs_path", rootfsPath),
		zap.String("tmp_mount", tmpMount),
	)

	err = unpackRootfs(ctx, l, img, tmpMount)
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

func unpackRootfs(ctx context.Context, l logger.Logger, srcImage containerregistry.Image, destDir string) (err error) {
	ctx, childSpan := tracer.Start(ctx, "unpack-rootfs")
	defer childSpan.End()

	ociPath, err := os.MkdirTemp("", "oci-image")
	if err != nil {
		return fmt.Errorf("while creating temporary file for squashed image: %w", err)
	}
	defer func() {
		go os.RemoveAll(ociPath)
	}()

	// Create export of layers in the temporary directory
	layers, err := createExport(ctx, l, srcImage, ociPath)
	if err != nil {
		return fmt.Errorf("while creating export of source image: %w", err)
	}

	// Mount the overlay filesystem with the extracted layers
	mountPath, err := os.MkdirTemp("", "overlayfs-mount")
	if err != nil {
		return fmt.Errorf("while creating temporary file for squashed image: %w", err)
	}
	defer func() {
		go os.RemoveAll(mountPath)
	}()

	err = filesystem.MountOverlayFS(ctx, layers, mountPath)
	if err != nil {
		return fmt.Errorf("while mounting overlayfs with layers: %w", err)
	}
	defer func() {
		if unmountErr := filesystem.Unmount(context.WithoutCancel(ctx), mountPath); unmountErr != nil {
			logger.L().Error(ctx, "error unmounting overlayfs mount point", zap.Error(unmountErr))
		}
	}()

	// List files in the mount point
	files, err := listFiles(ctx, mountPath)
	if err != nil {
		return fmt.Errorf("while listing files in overlayfs: %w", err)
	}
	l.Info(ctx, "Root filesystem structure: "+strings.Join(files, ", "))

	// Copy files from the overlayfs mount point to the destination directory
	err = copyFiles(ctx, mountPath, destDir)
	if err != nil {
		return fmt.Errorf("while copying files from overlayfs to destination directory: %w", err)
	}

	return nil
}

func listFiles(ctx context.Context, dir string) ([]string, error) {
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
func copyFiles(ctx context.Context, src, dest string) error {
	_, childSpan := tracer.Start(ctx, "copy-files")
	defer childSpan.End()

	// Does the following:
	// Recursion into directories
	// Symlinks
	// Permissions
	// Modification times
	// Group/owner (if possible)
	// Device files and special files
	// Hard links (-H)
	//
	// --whole-file: Copy files without using the delta algorithm, which is faster for local copies
	// --inplace: Update destination files in place, no need to create temporary files
	cmd := exec.CommandContext(ctx, "rsync", "-aH", "--whole-file", "--inplace", src+"/", dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("while copying files from %s to %s: %w: %s", src, dest, err, string(out))
	}

	return nil
}

// createExport extracts the layers of the source image into a temporary directory
// and returns the paths of the extracted layers. The layers are extracted in reverse order
// to maintain the correct order for overlayFS.
// The layers are extracted in parallel to speed up the process.
func createExport(ctx context.Context, logger logger.Logger, srcImage containerregistry.Image, path string) ([]string, error) {
	ctx, childSpan := tracer.Start(ctx, "create-oci-export")
	defer childSpan.End()

	layers, err := srcImage.Layers()
	if err != nil {
		return nil, fmt.Errorf("while getting layers of source image: %w", err)
	}

	layerPaths := make([]string, len(layers))
	var eg errgroup.Group
	for i, l := range layers {
		digest, err := l.Digest()
		if err != nil {
			return nil, fmt.Errorf("failed to get digest of layer %d: %w", i, err)
		}
		size, err := l.Size()
		if err != nil {
			return nil, fmt.Errorf("failed to get size of layer %d: %w", i, err)
		}
		telemetry.ReportEvent(ctx, "uncompressing layer",
			attribute.Int("layer.index", i),
			attribute.String("layer.digest", digest.String()),
			attribute.Int64("layer.size", size),
		)
		logger.Info(ctx, fmt.Sprintf("Uncompressing layer %s %s", digest, humanize.Bytes(uint64(size))))

		// Each layer has to be uniquely named, even if the digest is the same across different layers
		layerPath := filepath.Join(path, fmt.Sprintf("layer-%d-%s", i, strings.ReplaceAll(digest.String(), ":", "-")))
		// Layers need to be reported in reverse order to maintain the correct layer order for overlayfs
		layerPaths[len(layers)-i-1] = layerPath
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

			err = archive.Untar(rc, layerPath, &archive.TarOptions{
				IgnoreChownErrors: true,
			})
			if err != nil {
				return fmt.Errorf("failed to untar layer %d: %w", i, err)
			}

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("while extracting layers: %w", err)
	}

	logger.Info(ctx, "Layers extracted")

	return layerPaths, nil
}

func verifyImagePlatform(img containerregistry.Image, platform containerregistry.Platform) error {
	config, err := img.ConfigFile()
	if err != nil {
		return fmt.Errorf("error getting image config file: %w", err)
	}
	if config.Architecture != platform.Architecture {
		return fmt.Errorf("image is not %s", platform.Architecture)
	}

	return nil
}
