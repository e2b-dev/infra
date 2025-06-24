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
	containerregistry "github.com/google/go-containerregistry/pkg/v1"
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
) (i containerregistry.Image, e error) {
	ctx, span := tracer.Start(ctx, "build-layers")
	defer span.End()

	// Start the build engine runner
	buildEngine := NewDaggerEngine(ib.networkPool, ib.templateCache, ib.devicePool, ib.engineConfig)
	engineUrl, err := buildEngine.Start(ctx, tracer)
	if err != nil {
		return nil, fmt.Errorf("failed to start build engine: %w", err)
	}
	defer func() {
		go buildEngine.Stop(ctx, tracer)
	}()

	// Dagger Client
	logsBuffer := &bytes.Buffer{}
	defer func() {
		zap.L().Debug("Dagger logs",
			zap.String("logs", logsBuffer.String()),
			zap.Int("length", logsBuffer.Len()),
		)
	}()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(logsBuffer), dagger.WithRunnerHost(engineUrl))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Dagger: %w", err)
	}
	defer client.Close()

	// Set force for all steps after first step
	setForce := false
	for _, step := range ib.template.Steps {
		// Force rebuild if the step has a Force flag set to true
		if step.Force != nil && *step.Force {
			setForce = true
		} else {
			continue
		}

		force := setForce
		step.Force = &force
	}

	platform := containerregistry.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	// Find the last cached layer
	isCached := false
	hash, lastImg, err := findLastCachedLayer(ctx, tracer, ib.artifactRegistry, ib.template, platform)
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

	var layerSourceImagePath string
	var container *dagger.Container

	for i, step := range ib.template.Steps {
		layerIndex := i + 1
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		zap.L().Debug("building layer",
			zap.String("source_file_path", layerSourceImagePath),
			zap.String("command", cmd),
		)

		cached := ""
		if isCached {
			cached = "CACHED "
		}
		prefix := fmt.Sprintf("[builder %d/%d]", layerIndex, len(ib.template.Steps))
		postProcessor.WriteMsg(fmt.Sprintf("%s%s: %s", cached, prefix, cmd))

		// Process only the layers after the cached layer
		if isCached {
			if step.Hash == hash {
				isCached = false
			}
			continue
		}

		err := func() error {
			// Initialize the container only with the first layer, so we can skip the build when not needed
			if container == nil {
				authToken, err := ib.artifactRegistry.GetAuthToken(ctx)
				if err != nil {
					return fmt.Errorf("failed to get auth token: %w", err)
				}

				layerTag, err := ib.artifactRegistry.GetTag(ctx, ib.template.TemplateId, hash)
				if err != nil {
					return fmt.Errorf("failed to get layer tag: %w", err)
				}

				baseImage := ib.template.FromImage
				if hash != "" {
					baseImage = layerTag
				}

				pass := client.SetSecret("reg-pass", authToken.Password)
				container = client.Container().
					WithRegistryAuth(layerTag, authToken.Username, pass).
					From(baseImage)
			}

			c, err := ib.buildAndCacheLayer(
				ctx,
				tracer,
				postProcessor,
				client,
				container,
				prefix,
				step,
			)
			if err != nil {
				return err
			}
			container = c

			telemetry.ReportEvent(ctx, "built layer",
				attribute.String("layer.hash", step.Hash),
			)
			return nil
		}()
		if err != nil {
			return nil, fmt.Errorf("error building layer %d: %w", i+1, err)
		}
	}

	postProcessor.WriteMsg("All layers built successfully")

	if container == nil {
		// If no layers were built, return the source image, no need to re-export it again
		return img, nil
	}

	// // Export the Dagger last layer to a tarball
	// exportPath, err := os.MkdirTemp("", "last-layer-image-*")
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to create temporary file: %w", err)
	// }
	// // TODO: defer os.RemoveAll(exportPath)

	// export, err := container.Rootfs().Export(ctx, exportPath)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to export container: %w", err)
	// }

	// telemetry.ReportEvent(ctx, "exported image",
	// 	attribute.String("export", export),
	// )

	// rootfsLayer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
	// 	return archive.Tar(exportPath, archive.Uncompressed)
	// })
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to create layer from directory: %w", err)
	// }

	// img, err = mutate.AppendLayers(empty.Image, rootfsLayer)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to append exported layer: %w", err)
	// }

	return ib.artifactRegistry.GetLayer(ctx, ib.template.TemplateId, ib.template.Steps[len(ib.template.Steps)-1].Hash, platform)

	// return img, nil
}

func (ib *ImageBuilder) buildAndCacheLayer(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	client *dagger.Client,
	container *dagger.Container,
	prefix string,
	step *templatemanager.TemplateStep,
) (*dagger.Container, error) {
	ctx, span := tracer.Start(ctx, "build-layer")
	defer span.End()

	// Create working directory for the layer
	// This is used e.g. for ADD/COPY commands to extract files
	filesLayerHash := step.Hash
	if step.FilesHash != nil && *step.FilesHash != "" {
		filesLayerHash = *step.FilesHash
	}
	cachePath := filepath.Join(filesLayerCachePath, ib.template.TemplateId, filesLayerHash)
	if err := os.MkdirAll(cachePath, os.ModePerm); err != nil {
		return nil, fmt.Errorf("failed to create layer directory: %w", err)
	}
	// defer os.RemoveAll(cachePath)

	var err error
	container, err = ib.applyCommand(ctx, tracer, postProcessor, client, prefix, container, step, cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to apply command: %w", err)
	}

	layerTag, err := ib.artifactRegistry.GetTag(ctx, ib.template.TemplateId, step.Hash)
	if err != nil {
		return nil, fmt.Errorf("failed to get layer tag: %w", err)
	}

	telemetry.ReportEvent(ctx, "publishing layer",
		attribute.String("layer.hash", step.Hash),
		attribute.String("layer.tag", layerTag),
	)

	resp, err := container.
		Publish(ctx, layerTag)
	if err != nil {
		return nil, fmt.Errorf("failed to publish container: %w", err)
	}

	telemetry.ReportEvent(ctx, "layer published",
		attribute.String("layer_hash", step.Hash),
		attribute.String("layer.tag", layerTag),
		attribute.String("response", resp),
	)

	return container, nil
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
