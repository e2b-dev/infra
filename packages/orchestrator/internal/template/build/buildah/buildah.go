package buildah

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func From(ctx context.Context, tracer trace.Tracer, imagePath string) (string, error) {
	ctx, mountSpan := tracer.Start(ctx, "buildah-from")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "buildah", "from", imagePath)

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error running buildah from command: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

func Mount(ctx context.Context, tracer trace.Tracer, containerName string) (string, error) {
	ctx, mountSpan := tracer.Start(ctx, "buildah-mount")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "buildah", "mount", containerName)

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("error running buildah mount command: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

func Remove(ctx context.Context, tracer trace.Tracer, containerName string) error {
	ctx, mountSpan := tracer.Start(ctx, "buildah-rm")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "buildah", "rm", containerName)

	mountStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = mountStdoutWriter

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running buildah remove command: %w", err)
	}

	return nil
}

func RemoveImage(ctx context.Context, tracer trace.Tracer, imageName string) error {
	ctx, mountSpan := tracer.Start(ctx, "buildah-rm-image")
	defer mountSpan.End()

	cmd := exec.CommandContext(ctx, "buildah", "rmi", "-f", imageName)

	mountStdoutWriter := telemetry.NewEventWriter(ctx, "stdout")
	cmd.Stdout = mountStdoutWriter

	mountStderrWriter := telemetry.NewEventWriter(ctx, "stderr")
	cmd.Stderr = mountStderrWriter

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running buildah remove image command: %w", err)
	}

	return nil
}
