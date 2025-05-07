package build

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	ToMBShift = 20
	// Max size of the rootfs file in MB.
	maxRootfsSize = 15000 << ToMBShift
	cacheTimeout  = "48h"
)

var authConfig = registry.AuthConfig{
	Username: "_json_key_base64",
	Password: consts.GoogleServiceAccountSecret,
}

type Rootfs struct {
	client       *client.Client
	legacyClient *docker.Client

	env *Env
}

type MultiWriter struct {
	writers []io.Writer
}

func (mw *MultiWriter) Write(p []byte) (int, error) {
	for _, writer := range mw.writers {
		_, err := writer.Write(p)
		if err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

func NewRootfs(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, env *Env, docker *client.Client, legacyDocker *docker.Client) (*Rootfs, error) {
	childCtx, childSpan := tracer.Start(ctx, "new-rootfs")
	defer childSpan.End()

	rootfs := &Rootfs{
		client:       docker,
		legacyClient: legacyDocker,
		env:          env,
	}

	postProcessor.WriteMsg("Pulling Docker image...")
	err := rootfs.pullDockerImage(childCtx, tracer)
	if err != nil {
		errMsg := fmt.Errorf("error building docker image: %w", err)

		rootfs.cleanupDockerImage(childCtx, tracer)

		return nil, errMsg
	}
	postProcessor.WriteMsg("Pulled Docker image.")

	postProcessor.WriteMsg("Creating file system")
	err = rootfs.createRootfsFile(childCtx, tracer, postProcessor)
	if err != nil {
		errMsg := fmt.Errorf("error creating rootfs file: %w", err)

		rootfs.cleanupDockerImage(childCtx, tracer)

		return nil, errMsg
	}

	return rootfs, nil
}

func (r *Rootfs) pullDockerImage(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	authConfigBytes, err := json.Marshal(authConfig)
	if err != nil {
		errMsg := fmt.Errorf("error marshaling auth config: %w", err)

		return errMsg
	}

	authConfigBase64 := base64.URLEncoding.EncodeToString(authConfigBytes)
	if consts.DockerAuthConfig != "" {
		authConfigBase64 = consts.DockerAuthConfig
	}

	logs, err := r.client.ImagePull(childCtx, r.dockerTag(), image.PullOptions{
		RegistryAuth: authConfigBase64,
		Platform:     "linux/amd64",
	})
	if err != nil {
		errMsg := fmt.Errorf("error pulling image: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	_, err = io.Copy(os.Stdout, logs)
	if err != nil {
		errMsg := fmt.Errorf("error copying logs: %w", err)
		telemetry.ReportError(childCtx, errMsg)

		return errMsg
	}

	err = logs.Close()
	if err != nil {
		errMsg := fmt.Errorf("error closing logs: %w", err)
		telemetry.ReportError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "pulled image")

	return nil
}

func (r *Rootfs) cleanupDockerImage(ctx context.Context, tracer trace.Tracer) {
	childCtx, childSpan := tracer.Start(ctx, "cleanup-docker-image")
	defer childSpan.End()

	_, err := r.client.ImageRemove(childCtx, r.dockerTag(), image.RemoveOptions{
		Force:         false,
		PruneChildren: false,
	})
	if err != nil {
		errMsg := fmt.Errorf("error removing image: %w", err)
		telemetry.ReportError(childCtx, errMsg)
	} else {
		telemetry.ReportEvent(childCtx, "removed image")
	}
}

func (r *Rootfs) dockerTag() string {
	return fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s", consts.GCPRegion, consts.GCPProject, consts.DockerRegistry, r.env.TemplateId, r.env.BuildId)
}

func (r *Rootfs) createRootfsFile(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor) error {
	childCtx, childSpan := tracer.Start(ctx, "create-rootfs-file")
	defer childSpan.End()

	var scriptDef bytes.Buffer

	err := EnvInstanceTemplate.Execute(&scriptDef, struct {
		EnvID       string
		BuildID     string
		StartCmd    string
		FcAddress   string
		MemoryLimit int
	}{
		FcAddress:   fcAddr,
		EnvID:       r.env.TemplateId,
		BuildID:     r.env.BuildId,
		StartCmd:    strings.ReplaceAll(r.env.StartCmd, "'", "\\'"),
		MemoryLimit: int(math.Min(float64(r.env.MemoryMB)/2, 512)),
	})
	if err != nil {
		errMsg := fmt.Errorf("error executing provision script: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "executed provision script env")

	pidsLimit := int64(200)
	memory := max(r.env.MemoryMB, 512) << ToMBShift

	cont, err := r.client.ContainerCreate(childCtx, &container.Config{
		Image:        r.dockerTag(),
		Entrypoint:   []string{"/bin/bash", "-c"},
		User:         "root",
		Cmd:          []string{scriptDef.String()},
		Tty:          false,
		AttachStdout: true,
		AttachStderr: true,
	}, &container.HostConfig{
		SecurityOpt: []string{"no-new-privileges"},
		CapAdd:      []string{"CHOWN", "DAC_OVERRIDE", "FSETID", "FOWNER", "SETGID", "SETUID", "NET_RAW", "SYS_CHROOT"},
		CapDrop:     []string{"ALL"},
		// TODO: Network mode is causing problems with /etc/hosts - we want to find a way to fix this and enable network mode again
		// NetworkMode: container.NetworkMode(network.ID),
		Resources: container.Resources{
			Memory:     memory,
			CPUPeriod:  100000,
			CPUQuota:   r.env.VCpuCount * 100000,
			MemorySwap: memory,
			PidsLimit:  &pidsLimit,
		},
	}, nil, &v1.Platform{}, "")
	if err != nil {
		errMsg := fmt.Errorf("error creating container: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "created container")

	defer func() {
		go func() {
			cleanupContext, cleanupSpan := tracer.Start(
				trace.ContextWithSpanContext(context.Background(), childSpan.SpanContext()),
				"cleanup-container",
			)
			defer cleanupSpan.End()

			removeErr := r.legacyClient.RemoveContainer(docker.RemoveContainerOptions{
				ID:            cont.ID,
				RemoveVolumes: true,
				Force:         true,
				Context:       cleanupContext,
			})
			if removeErr != nil {
				errMsg := fmt.Errorf("error removing container: %w", removeErr)
				telemetry.ReportError(cleanupContext, errMsg)
			} else {
				telemetry.ReportEvent(cleanupContext, "removed container")
			}

			// Move pruning to separate goroutine
			cacheTimeoutArg := filters.Arg("until", cacheTimeout)

			_, pruneErr := r.client.BuildCachePrune(cleanupContext, types.BuildCachePruneOptions{
				Filters: filters.NewArgs(cacheTimeoutArg),
				All:     true,
			})
			if pruneErr != nil {
				errMsg := fmt.Errorf("error pruning build cache: %w", pruneErr)
				telemetry.ReportError(cleanupContext, errMsg)
			} else {
				telemetry.ReportEvent(cleanupContext, "pruned build cache")
			}

			_, pruneErr = r.client.ImagesPrune(cleanupContext, filters.NewArgs(cacheTimeoutArg))
			if pruneErr != nil {
				errMsg := fmt.Errorf("error pruning images: %w", pruneErr)
				telemetry.ReportError(cleanupContext, errMsg)
			} else {
				telemetry.ReportEvent(cleanupContext, "pruned images")
			}

			_, pruneErr = r.client.ContainersPrune(cleanupContext, filters.NewArgs(cacheTimeoutArg))
			if pruneErr != nil {
				errMsg := fmt.Errorf("error pruning containers: %w", pruneErr)
				telemetry.ReportError(cleanupContext, errMsg)
			} else {
				telemetry.ReportEvent(cleanupContext, "pruned containers")
			}
		}()
	}()

	filesToTar := []fileToTar{
		{
			localPath: storage.HostOldEnvdPath,
			tarPath:   storage.GuestOldEnvdPath,
		},
		{
			localPath: storage.HostEnvdPath,
			tarPath:   storage.GuestEnvdPath,
		},
	}

	pr, pw := io.Pipe()

	go func() {
		var errMsg error
		defer func() {
			closeErr := pw.CloseWithError(errMsg)
			if closeErr != nil {
				errMsg := fmt.Errorf("error closing pipe: %w", closeErr)
				telemetry.ReportCriticalError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "closed pipe")
			}
		}()

		tw := tar.NewWriter(pw)
		defer func() {
			err = tw.Close()
			if err != nil {
				errMsg = fmt.Errorf("error closing tar writer: %w", errors.Join(err, errMsg))
				telemetry.ReportCriticalError(childCtx, errMsg)
			} else {
				telemetry.ReportEvent(childCtx, "closed tar writer")
			}
		}()

		for _, file := range filesToTar {
			addErr := addFileToTarWriter(tw, file)
			if addErr != nil {
				errMsg = fmt.Errorf("error adding envd to tar writer: %w", addErr)
				telemetry.ReportCriticalError(childCtx, errMsg)

				break
			} else {
				telemetry.ReportEvent(childCtx, "added envd to tar writer")
			}
		}
	}()

	// Copy tar to the container
	err = r.legacyClient.UploadToContainer(cont.ID, docker.UploadToContainerOptions{
		InputStream:          pr,
		Path:                 "/",
		Context:              childCtx,
		NoOverwriteDirNonDir: false,
	})
	if err != nil {
		errMsg := fmt.Errorf("error copying envd to container: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "copied envd to container")

	postProcessor.WriteMsg("Provisioning template")
	err = r.client.ContainerStart(childCtx, cont.ID, container.StartOptions{})
	if err != nil {
		errMsg := fmt.Errorf("error starting container: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "started container")

	go func() {
		anonymousChildCtx, anonymousChildSpan := tracer.Start(childCtx, "handle-container-logs", trace.WithSpanKind(trace.SpanKindConsumer))
		defer anonymousChildSpan.End()

		containerStdoutWriter := telemetry.NewEventWriter(anonymousChildCtx, "stdout")
		containerStderrWriter := telemetry.NewEventWriter(anonymousChildCtx, "stderr")

		outWriter := &MultiWriter{
			writers: []io.Writer{containerStdoutWriter, postProcessor},
		}
		errWriter := &MultiWriter{
			writers: []io.Writer{containerStderrWriter, r.env.BuildLogsWriter, postProcessor},
		}

		logsErr := r.legacyClient.Logs(docker.LogsOptions{
			Stdout:       true,
			Stderr:       true,
			RawTerminal:  false,
			OutputStream: outWriter,
			ErrorStream:  errWriter,
			Context:      childCtx,
			Container:    cont.ID,
			Follow:       true,
			Timestamps:   false,
		})
		if logsErr != nil {
			errMsg := fmt.Errorf("error getting container logs: %w", logsErr)
			telemetry.ReportError(anonymousChildCtx, errMsg)
		} else {
			telemetry.ReportEvent(anonymousChildCtx, "setup container logs")
		}
	}()

	wait, errWait := r.client.ContainerWait(childCtx, cont.ID, container.WaitConditionNotRunning)
	select {
	case <-childCtx.Done():
		errMsg := fmt.Errorf("error waiting for container: %w", childCtx.Err())
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	case waitErr := <-errWait:
		if waitErr != nil {
			errMsg := fmt.Errorf("error waiting for container: %w", waitErr)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return errMsg
		}
	case response := <-wait:
		if response.Error != nil {
			errMsg := fmt.Errorf("error waiting for container - code %d: %s", response.StatusCode, response.Error.Message)
			telemetry.ReportCriticalError(childCtx, errMsg)

			return errMsg
		}
	}

	telemetry.ReportEvent(childCtx, "waited for container exit")

	inspection, err := r.client.ContainerInspect(ctx, cont.ID)
	if err != nil {
		errMsg := fmt.Errorf("error inspecting container: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "inspected container")

	if inspection.State.Running {
		errMsg := fmt.Errorf("container is still running")
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	if inspection.State.ExitCode != 0 {
		errMsg := fmt.Errorf("container exited with status %d: %s", inspection.State.ExitCode, inspection.State.Error)
		telemetry.ReportCriticalError(
			childCtx,
			errMsg,
			attribute.Int("exit_code", inspection.State.ExitCode),
			attribute.String("error", inspection.State.Error),
			attribute.Bool("oom", inspection.State.OOMKilled),
		)

		return errMsg
	}

	postProcessor.WriteMsg("Extracting file system")
	rootfsFile, err := os.Create(r.env.BuildRootfsPath())
	if err != nil {
		errMsg := fmt.Errorf("error creating rootfs file: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "created rootfs file")

	defer func() {
		rootfsErr := rootfsFile.Close()
		if rootfsErr != nil {
			errMsg := fmt.Errorf("error closing rootfs file: %w", rootfsErr)
			telemetry.ReportError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "closed rootfs file")
		}
	}()

	pr, pw = io.Pipe()

	go func() {
		downloadErr := r.legacyClient.DownloadFromContainer(cont.ID, docker.DownloadFromContainerOptions{
			Context:      childCtx,
			Path:         "/",
			OutputStream: pw,
		})
		if downloadErr != nil {
			errMsg := fmt.Errorf("error downloading from container: %w", downloadErr)
			telemetry.ReportCriticalError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "downloaded from container")
		}

		closeErr := pw.Close()
		if closeErr != nil {
			errMsg := fmt.Errorf("error closing pipe: %w", closeErr)
			telemetry.ReportCriticalError(childCtx, errMsg)
		} else {
			telemetry.ReportEvent(childCtx, "closed pipe")
		}
	}()

	telemetry.ReportEvent(childCtx, "coverting tar to ext4")

	// This package creates a read-only ext4 filesystem from a tar archive.
	// We need to use another program to make the filesystem writable.
	err = tar2ext4.ConvertTarToExt4(pr, rootfsFile, tar2ext4.MaximumDiskSize(maxRootfsSize))
	if err != nil {
		if strings.Contains(err.Error(), "disk exceeded maximum size") {
			r.env.BuildLogsWriter.Write([]byte(fmt.Sprintf("Build failed - exceeded maximum size %v MB.\n", maxRootfsSize>>ToMBShift)))
		}

		errMsg := fmt.Errorf("error converting tar to ext4: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	postProcessor.WriteMsg("Filesystem cleanup")
	telemetry.ReportEvent(childCtx, "converted container tar to ext4")

	tuneContext, tuneSpan := tracer.Start(childCtx, "tune-rootfs-file-cmd")
	defer tuneSpan.End()

	cmd := exec.CommandContext(tuneContext, "tune2fs", "-O ^read-only", r.env.BuildRootfsPath())

	tuneStdoutWriter := telemetry.NewEventWriter(tuneContext, "stdout")
	cmd.Stdout = tuneStdoutWriter

	tuneStderrWriter := telemetry.NewEventWriter(childCtx, "stderr")
	cmd.Stderr = tuneStderrWriter

	err = cmd.Run()
	if err != nil {
		errMsg := fmt.Errorf("error making rootfs file writable: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "made rootfs file writable")

	rootfsStats, err := rootfsFile.Stat()
	if err != nil {
		errMsg := fmt.Errorf("error statting rootfs file: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "statted rootfs file")

	// In bytes
	rootfsSize := rootfsStats.Size() + r.env.DiskSizeMB<<ToMBShift

	r.env.rootfsSize = rootfsSize

	err = rootfsFile.Truncate(rootfsSize)
	if err != nil {
		errMsg := fmt.Errorf("error truncating rootfs file: %w to size of build + defaultDiskSizeMB", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "truncated rootfs file to size of build + defaultDiskSizeMB")

	resizeContext, resizeSpan := tracer.Start(childCtx, "resize-rootfs-file-cmd")
	defer resizeSpan.End()

	cmd = exec.CommandContext(resizeContext, "resize2fs", r.env.BuildRootfsPath())

	resizeStdoutWriter := telemetry.NewEventWriter(resizeContext, "stdout")
	cmd.Stdout = resizeStdoutWriter

	resizeStderrWriter := telemetry.NewEventWriter(resizeContext, "stderr")
	cmd.Stderr = resizeStderrWriter

	err = cmd.Run()
	if err != nil {
		errMsg := fmt.Errorf("error resizing rootfs file: %w", err)
		telemetry.ReportCriticalError(childCtx, errMsg)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "resized rootfs file")

	return nil
}
