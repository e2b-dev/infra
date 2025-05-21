package oci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Microsoft/hcsshim/ext4/tar2ext4"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const ToMBShift = 20

var authConfig = authn.Basic{
	Username: "_json_key_base64",
	Password: consts.GoogleServiceAccountSecret,
}

func getRepositoryAuth() (authn.Authenticator, error) {
	authCfg := consts.DockerAuthConfig
	if authCfg == "" {
		return &authConfig, nil
	}

	decoded, err := base64.URLEncoding.DecodeString(authCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode auth config: %w", err)
	}

	var cfg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON auth config: %w", err)
	}

	return &authn.Basic{
		Username: cfg.Username,
		Password: cfg.Password,
	}, nil
}

func PullImage(ctx context.Context, tracer trace.Tracer, dockerTag string) (string, error) {
	_, span := tracer.Start(ctx, "buildah-pull-image")
	defer span.End()

	auth, err := getRepositoryAuth()
	if err != nil {
		return "", fmt.Errorf("failed to get repository auth: %w", err)
	}

	var args []string

	if basicAuth, ok := auth.(*authn.Basic); ok {
		args = append(args, "--creds", fmt.Sprintf("%s:%s", basicAuth.Username, basicAuth.Password))
	} else {
		return "", fmt.Errorf("unsupported authenticator type: %T", auth)
	}

	args = append(args, dockerTag)

	cmd := exec.CommandContext(ctx, "buildah", append([]string{"pull"}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("buildah pull failed: %w\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

func MountImage(ctx context.Context, tracer trace.Tracer, image string) (string, error) {
	_, span := tracer.Start(ctx, "buildah-mount-image")
	defer span.End()

	// Create a container from the image
	createCmd := exec.CommandContext(ctx, "buildah", "from", image)
	containerIDBytes, err := createCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("buildah from failed: %w\nOutput: %s", err, string(containerIDBytes))
	}
	containerID := strings.TrimSpace(string(containerIDBytes))

	// Mount container
	mountCmd := exec.CommandContext(ctx, "buildah", "mount", containerID)
	mountOutput, err := mountCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("buildah mount failed: %w\nOutput: %s", err, string(mountOutput))
	}

	return strings.TrimSpace(string(mountOutput)), nil
}

func GetImage(ctx context.Context, tracer trace.Tracer, dockerTag string) (v1.Image, error) {
	childCtx, childSpan := tracer.Start(ctx, "pull-docker-image")
	defer childSpan.End()

	auth, err := getRepositoryAuth()
	if err != nil {
		return nil, fmt.Errorf("failed to get auth: %w", err)
	}

	ref, err := name.ParseReference(dockerTag)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	platform := v1.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}
	img, err := remote.Image(ref, remote.WithAuth(auth), remote.WithPlatform(platform))
	if err != nil {
		return nil, fmt.Errorf("error pulling image: %w", err)
	}

	telemetry.ReportEvent(childCtx, "pulled image")
	return img, nil
}

func GetImageSize(img v1.Image) (int64, error) {
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

func ToExt4(ctx context.Context, img v1.Image, rootfsPath string, sizeLimit int64) error {
	pr := mutate.Extract(img)
	defer pr.Close()

	rootfsFile, err := os.Create(rootfsPath)
	if err != nil {
		return fmt.Errorf("error creating rootfs file: %w", err)
	}
	defer func() {
		rootfsErr := rootfsFile.Close()
		if rootfsErr != nil {
			telemetry.ReportError(ctx, fmt.Errorf("error closing rootfs file: %w", rootfsErr))
		} else {
			telemetry.ReportEvent(ctx, "closed rootfs file")
		}
	}()

	// Convert tar to ext4 image
	if err := tar2ext4.Convert(pr, rootfsFile, tar2ext4.ConvertWhiteout, tar2ext4.MaximumDiskSize(sizeLimit)); err != nil {
		if strings.Contains(err.Error(), "disk exceeded maximum size") {
			return fmt.Errorf("build failed - exceeded maximum size %v MB", sizeLimit>>ToMBShift)
		}
		return fmt.Errorf("error converting tar to ext4: %w", err)
	}

	return nil
}

func ToExt4FromMount(ctx context.Context, mountPath string, outputPath string, diskSize int64) error {
	diskSizeMB := diskSize >> ToMBShift

	// Step 1: Create sparse file
	cmd := exec.Command(
		"dd",
		"if=/dev/zero",
		"of="+outputPath,
		"bs=1M",
		"count=0",
		fmt.Sprintf("seek=%d", diskSizeMB),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create sparse file: %w\nOutput: %s", err, string(output))
	}
	zap.L().Debug("created sparse file")

	// Step 2: Format with ext4
	if err := exec.Command("mkfs.ext4", "-F", outputPath).Run(); err != nil {
		return fmt.Errorf("failed to mkfs.ext4: %w", err)
	}
	zap.L().Debug("formatted ext4")

	// Step 4: Mount device ext4
	tmpMountDir := filepath.Join(os.TempDir(), "ext4-mount", uuid.New().String())
	if err := os.MkdirAll(tmpMountDir, 0755); err != nil {
		return fmt.Errorf("failed to create mount dir: %w", err)
	}
	defer os.RemoveAll(tmpMountDir)

	cmd = exec.Command("mount", "-o", "loop", outputPath, tmpMountDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to mount ext4 image: %w", err)
	}
	defer exec.Command("umount", tmpMountDir).Run()
	zap.L().Debug("mounted ext4")

	// Step 5: Copy files into new ext4
	if err := exec.Command("rsync", "-aAX", "--inplace", "--whole-file", "--exclude={\"/proc/*\",\"/tmp/*\",\"/dev/*\"}", mountPath+"/", tmpMountDir+"/").Run(); err != nil {
		return fmt.Errorf("failed to rsync rootfs: %w", err)
	}
	zap.L().Debug("files copied")

	return nil
}
