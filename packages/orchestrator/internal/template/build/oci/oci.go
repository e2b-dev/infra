package oci

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
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

	r := mutate.Extract(img)
	defer r.Close()

	tr := tar.NewReader(r)
	err = extractTarToDir(tr, tmpMount)
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

func extractTarToDir(tr *tar.Reader, targetDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		outPath := filepath.Join(targetDir, hdr.Name)

		// Sanitize a path to avoid traversal
		if !strings.HasPrefix(filepath.Clean(outPath), filepath.Clean(targetDir)) {
			return fmt.Errorf("illegal file path: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(outPath, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// Ensure parent directory exists
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return err
			}
			if _, err := os.Lstat(outPath); err == nil {
				os.Remove(outPath)
			}
			if err := os.Symlink(hdr.Linkname, outPath); err != nil {
				return err
			}
		}
	}
	return nil
}
