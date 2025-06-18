package builder

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/google/go-containerregistry/pkg/crane"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/templateconfig"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const filesLayerCachePath = "/orchestrator/builder/files-cache"

type MissingFilesError struct {
	Steps []*templatemanager.TemplateStep
}

func (e *MissingFilesError) Error() string {
	return fmt.Sprintf("missing files for steps: %d", len(e.Steps))
}

type ImageBuilder struct {
	artifactRegistry artifactsregistry.ArtifactsRegistry
	storage          storage.StorageProvider

	networkPool   *network.Pool
	templateCache *template.Cache
	devicePool    *nbd.DevicePool

	template *templateconfig.TemplateConfig
}

func NewImageBuilder(
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	storage storage.StorageProvider,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
	template *templateconfig.TemplateConfig,
) *ImageBuilder {
	return &ImageBuilder{
		artifactRegistry: artifactRegistry,
		storage:          storage,
		networkPool:      networkPool,
		templateCache:    templateCache,
		devicePool:       devicePool,
		template:         template,
	}
}

func (ib *ImageBuilder) BuildLayers(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	img containerregistry.Image,
) (path string, e error) {
	ctx, span := tracer.Start(ctx, "build-layers")
	defer span.End()

	// Start the build engine runner
	buildEngine := NewDaggerEngine(ib.networkPool, ib.templateCache, ib.devicePool)
	engineUrl, err := buildEngine.Start(ctx, tracer)
	if err != nil {
		return "", fmt.Errorf("failed to start build engine: %w", err)
	}
	defer buildEngine.Stop(ctx, tracer)

	// Dagger Client
	err = os.Setenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST", engineUrl)
	if err != nil {
		return "", fmt.Errorf("failed to set Dagger environment variable: %w", err)
	}
	logsBuffer := &bytes.Buffer{}
	defer func() {
		zap.L().Debug("Dagger logs",
			zap.String("logs", logsBuffer.String()),
			zap.Int("length", logsBuffer.Len()),
		)
	}()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logsBuffer))
	if err != nil {
		return "", fmt.Errorf("failed to connect to Dagger: %w", err)
	}
	defer client.Close()

	// Find the last cached layer
	isCached := false
	hash, lastImg, err := findLastCachedLayer(ctx, tracer, ib.artifactRegistry, ib.template)
	if err == nil {
		postProcessor.WriteMsg(fmt.Sprintf("Found last cached layer: %s", hash))
		zap.L().Debug("found last cached layer",
			zap.String("hash", hash),
		)
		// Use the last cached layer as the source image for the next layer
		img = lastImg
		isCached = true
	} else {
		postProcessor.WriteMsg("No cached layers found")
		zap.L().Debug("no cached layers found", zap.Error(err))
	}

	// Extract the source layer image to a temporary file
	layerSourceImage, err := os.CreateTemp("", "layer-image-*.tar")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer layerSourceImage.Close()
	err = crane.Save(img, uuid.New().String(), layerSourceImage.Name())
	if err != nil {
		return "", fmt.Errorf("failed to write source image to temporary file: %w", err)
	}
	layerSourceImagePath := layerSourceImage.Name()

	for i, step := range ib.template.Steps {
		// Force rebuild if the step has a Force flag set to true
		if step.Force != nil && *step.Force {
			isCached = false
		}

		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		zap.L().Debug("building layer",
			zap.String("source_file_path", layerSourceImagePath),
			zap.String("command", cmd),
		)

		cached := ""
		if isCached {
			cached = "CACHED "
		}
		prefix := fmt.Sprintf("[builder %d/%d]", i+1, len(ib.template.Steps))
		postProcessor.WriteMsg(fmt.Sprintf("%s%s: %s", cached, prefix, cmd))

		// Process only the layers after the cached layer
		if isCached {
			if step.Hash == hash {
				isCached = false
			}
			continue
		}

		err := func() error {
			defer os.Remove(layerSourceImagePath)
			layerOutputImage, err := os.CreateTemp("", "layer-image-*.tar")
			if err != nil {
				return fmt.Errorf("failed to create temporary file: %w", err)
			}
			defer layerOutputImage.Close()
			layerOutputImagePath := layerOutputImage.Name()

			_, err = ib.buildAndCacheLayer(
				ctx,
				tracer,
				postProcessor,
				client,
				prefix,
				layerSourceImagePath,
				layerOutputImagePath,
				step,
			)
			if err != nil {
				return err
			}

			zap.L().Debug("built layer",
				zap.String("layer_hash", step.Hash),
				zap.String("layer_source_image", layerSourceImagePath),
				zap.String("layer_output_image", layerOutputImagePath),
			)

			layerSourceImagePath = layerOutputImagePath
			return nil
		}()
		if err != nil {
			return "", fmt.Errorf("error building layer %d: %w", i+1, err)
		}
	}

	return layerSourceImagePath, nil
}

func (ib *ImageBuilder) buildAndCacheLayer(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	client *dagger.Client,
	prefix string,
	sourceFilePath string,
	targetFilePath string,
	step *templatemanager.TemplateStep,
) (string, error) {
	ctx, span := tracer.Start(ctx, "build-layer")
	defer span.End()

	sourceLayer := client.Host().File(sourceFilePath)
	container := client.Container().
		Import(sourceLayer)

	var err error
	container, err = ib.applyCommand(ctx, tracer, postProcessor, client, prefix, container, step)
	if err != nil {
		return "", fmt.Errorf("failed to apply command: %w", err)
	}

	export, err := container.Export(ctx, targetFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to export container: %w", err)
	}

	zap.L().Debug("exported layer",
		zap.String("source_file_path", sourceFilePath),
		zap.String("target_file_path", targetFilePath),
		zap.String("command_type", step.Type),
		zap.Strings("command_args", step.Args),
		zap.String("export", export),
	)

	img, err := tarball.ImageFromPath(targetFilePath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to read image from build export: %w", err)
	}

	err = ib.artifactRegistry.PushLayer(ctx, ib.template.TemplateId, step.Hash, img)
	if err != nil {
		// Soft fail, the build can continue even if the layer push fails
		zap.L().Error("failed to push layer to artifact registry", zap.Error(err))
	} else {
		zap.L().Debug("pushed layer",
			zap.String("source_file_path", sourceFilePath),
			zap.String("target_file_path", targetFilePath),
			zap.String("command_type", step.Type),
			zap.Strings("command_args", step.Args),
		)
	}

	return step.Hash, nil
}

func (ib *ImageBuilder) applyCommand(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	client *dagger.Client,
	prefix string,
	container *dagger.Container,
	step *templatemanager.TemplateStep,
) (*dagger.Container, error) {
	ctx, span := tracer.Start(ctx, "apply-command")
	defer span.End()

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ADD":
		// args: [localPath containerPath]
		fallthrough
	case "COPY":
		// args: [localPath containerPath]
		if len(args) != 2 {
			return nil, fmt.Errorf("%s requires [localPath containerPath]", cmdType)
		}
		if step.FilesHash == nil || *step.FilesHash == "" {
			return nil, fmt.Errorf("%s requires files hash to be set", cmdType)
		}

		obj, err := ib.storage.OpenObject(ctx, GetLayerFilesCachePath(ib.template.TemplateId, *step.FilesHash))
		if err != nil {
			return nil, fmt.Errorf("failed to open files object from storage: %w", err)
		}

		cachePath := filepath.Join(filesLayerCachePath, ib.template.TemplateId, *step.FilesHash)
		if err := os.MkdirAll(cachePath, os.ModePerm); err != nil {
			return nil, fmt.Errorf("failed to create directory for file: %w", err)
		}
		defer os.RemoveAll(cachePath)

		pr, pw := io.Pipe()
		// Start writing tar data to the pipe writer in a goroutine
		go func() {
			defer pw.Close()
			if _, err := obj.WriteTo(pw); err != nil {
				pw.CloseWithError(err)
			}
		}()

		// Extract the tar file to the specified directory
		if err := untar(pr, cachePath); err != nil {
			return nil, fmt.Errorf("failed to untar contents: %w", err)
		}

		if args[0] == "." {
			// If the local path is ".", use the directory
			dir := client.Host().Directory(cachePath)
			return container.WithDirectory(args[1], dir), nil
		} else {
			// Otherwise, copy just the specified file
			f := client.Host().File(cachePath)
			return container.WithFile(args[1], f), nil
		}
	case "ARG":
		// args: [key value]
		if len(args) != 2 {
			return nil, fmt.Errorf("ARG requires [key value]")
		}
		return container.WithEnvVariable(args[0], args[1]), nil

	case "ENV":
		// args: [key value]
		if len(args) != 2 {
			return nil, fmt.Errorf("ENV requires [key value]")
		}
		return container.WithEnvVariable(args[0], args[1]), nil

	case "RUN":
		// args: command and args, e.g., ["sh", "-c", "echo hi"]
		if len(args) == 0 {
			return nil, fmt.Errorf("RUN requires command arguments")
		}
		c := container.WithExec(args, dagger.ContainerWithExecOpts{
			Expand: true,
		})

		// Show the output of the command
		stderr, err := c.Stderr(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get container stderr: %w", err)
		}
		stdout, err := c.Stdout(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get container stdout: %w", err)
		}
		if stderr != "" {
			postProcessor.WriteMsg(fmt.Sprintf("%s [stderr]: %s", prefix, stderr))
		}
		if stdout != "" {
			postProcessor.WriteMsg(fmt.Sprintf("%s [stdout]: %s", prefix, stdout))
		}
		zap.L().Debug("container output",
			zap.String("stdout", stdout),
			zap.String("stderr", stderr),
		)
		return c, nil
	case "USER":
		// args: [username]
		if len(args) != 1 {
			return nil, fmt.Errorf("USER requires [username]")
		}
		return container.WithUser(args[0]), nil

	case "WORKDIR":
		// args: [path]
		if len(args) != 1 {
			return nil, fmt.Errorf("WORKDIR requires [path]")
		}
		return container.WithWorkdir(args[0]), nil

	default:
		return nil, fmt.Errorf("unsupported command type: %s", cmdType)
	}
}

// untar extracts a tar archive from the reader `r` into the directory `destDir`.
// It preserves file permissions and structure, while preventing path traversal and symlink attacks.
func untar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)

	destDir = filepath.Clean(destDir)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return fmt.Errorf("error reading tar entry: %w", err)
		}

		name := filepath.Clean(hdr.Name)
		targetPath := filepath.Join(destDir, name)

		// Prevent path traversal
		if !strings.HasPrefix(targetPath, destDir+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("mkdir failed for %s: %w", targetPath, err)
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("mkdir for file parent failed: %w", err)
			}

			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create file %s failed: %w", targetPath, err)
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tr); err != nil {
				return fmt.Errorf("copy to %s failed: %w", targetPath, err)
			}

		case tar.TypeSymlink:
			linkTarget := hdr.Linkname
			linkPath := filepath.Join(filepath.Dir(targetPath), linkTarget)
			resolvedPath := filepath.Clean(linkPath)

			if !strings.HasPrefix(resolvedPath, destDir+string(os.PathSeparator)) {
				return fmt.Errorf("symlink %s points outside destination", hdr.Linkname)
			}

			if err := os.Symlink(linkTarget, targetPath); err != nil {
				return fmt.Errorf("symlink %s -> %s failed: %w", targetPath, linkTarget, err)
			}

		default:
			// skipping unsupported tar entry: %s (type: %c)", hdr.Name, hdr.Typeflag
			continue
		}
	}

	return nil
}
