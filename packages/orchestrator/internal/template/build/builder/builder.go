package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/containers/storage/pkg/archive"
	"github.com/google/go-containerregistry/pkg/crane"
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
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
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

	template     *templateconfig.TemplateConfig
	engineConfig *templatemanager.EngineConfig
}

func NewImageBuilder(
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	storage storage.StorageProvider,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
	template *templateconfig.TemplateConfig,
	engineConfig *templatemanager.EngineConfig,
) *ImageBuilder {
	return &ImageBuilder{
		artifactRegistry: artifactRegistry,
		storage:          storage,
		networkPool:      networkPool,
		templateCache:    templateCache,
		devicePool:       devicePool,
		template:         template,
		engineConfig:     engineConfig,
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
	buildEngine := NewDaggerEngine(ib.networkPool, ib.templateCache, ib.devicePool, ib.engineConfig)
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
	telemetry.ReportEvent(ctx, "saved source image to temporary file",
		attribute.String("temporary_file_path", layerSourceImagePath),
	)

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

			telemetry.ReportEvent(ctx, "built layer",
				attribute.String("layer_hash", step.Hash),
				attribute.String("layer_source_image", layerSourceImagePath),
				attribute.String("layer_output_image", layerOutputImagePath),
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

	// Create working directory for the layer
	// This is used e.g. for ADD/COPY commands to extract files
	filesLayerHash := step.Hash
	if step.FilesHash != nil && *step.FilesHash != "" {
		filesLayerHash = *step.FilesHash
	}
	cachePath := filepath.Join(filesLayerCachePath, ib.template.TemplateId, filesLayerHash)
	if err := os.MkdirAll(cachePath, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create layer directory: %w", err)
	}
	defer os.RemoveAll(cachePath)

	var err error
	container, err = ib.applyCommand(ctx, tracer, postProcessor, client, prefix, container, step, cachePath)
	if err != nil {
		return "", fmt.Errorf("failed to apply command: %w", err)
	}

	// TODO: Takes 6 seconds to export a layer, can we optimize this?
	export, err := container.Export(ctx, targetFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to export container: %w", err)
	}

	telemetry.ReportEvent(ctx, "exported layer",
		attribute.String("source_file_path", sourceFilePath),
		attribute.String("target_file_path", targetFilePath),
		attribute.String("command_type", step.Type),
		attribute.StringSlice("command_args", step.Args),
		attribute.String("export", export),
	)

	img, err := tarball.ImageFromPath(targetFilePath, nil)
	if err != nil {
		return "", fmt.Errorf("failed to read image from build export: %w", err)
	}

	telemetry.ReportEvent(ctx, "loaded layer from path",
		attribute.String("source_file_path", sourceFilePath),
		attribute.String("target_file_path", targetFilePath),
		attribute.String("command_type", step.Type),
		attribute.StringSlice("command_args", step.Args),
		attribute.String("export", export),
	)

	go func() {
		err = ib.artifactRegistry.PushLayer(ctx, ib.template.TemplateId, step.Hash, img)
		if err != nil {
			// Soft fail, the build can continue even if the layer push fails
			telemetry.ReportError(ctx, "failed to push layer to artifact registry", err)
		} else {
			telemetry.ReportEvent(ctx, "pushed layer",
				attribute.String("source_file_path", sourceFilePath),
				attribute.String("target_file_path", targetFilePath),
				attribute.String("command_type", step.Type),
				attribute.StringSlice("command_args", step.Args),
			)
		}
	}()

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
	cachePath string,
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

		pr, pw := io.Pipe()
		// Start writing tar data to the pipe writer in a goroutine
		go func() {
			defer pw.Close()
			if _, err := obj.WriteTo(pw); err != nil {
				pw.CloseWithError(err)
			}
		}()

		// Extract the tar file to the specified directory
		if err := archive.Untar(pr, cachePath, &archive.TarOptions{}); err != nil {
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
