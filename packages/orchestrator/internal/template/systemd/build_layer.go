package systemd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"go.uber.org/zap"
)

const (
	imageTag               = "ubuntu-with-systemd"
	fullImageOutputTarPath = "ubuntu-with-systemd.tar"
	tarFileName            = "/systemd-raw.tar"
	cleanedFileName        = "/systemd.tar"

	dockerfileContent = `FROM ubuntu:20.04

RUN DEBIAN_FRONTEND=noninteractive apt-get update && apt-get install -y --no-install-recommends \
  systemd systemd-sysv && \
  apt-get clean && \
  rm -rf \
    /var/lib/apt/lists/* \
    /var/lib/dpkg/* \
    /var/cache/* \
    /var/log/* \
    /etc/machine-id
`
)

// BuildLayer builds the Docker image with systemd and extracts the final layer without whiteout files.
func BuildLayer() error {
	zap.L().Info("Building ubuntu with systemd layer")

	err := buildDockerImageTar(imageTag, fullImageOutputTarPath)
	if err != nil {
		return fmt.Errorf("failed to build docker image: %w", err)
	}
	defer os.Remove(fullImageOutputTarPath)

	// Get the image reference
	ref, err := name.NewTag(imageTag)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}
	img, err := tarball.ImageFromPath(fullImageOutputTarPath, &ref)
	if err != nil {
		return fmt.Errorf("failed to get image from daemon: %w", err)
	}

	// Get the last layer
	lastLayer, err := getLastLayerFromImage(img)
	if err != nil {
		return fmt.Errorf("failed to get last layer: %w", err)
	}

	// Stream the data from the layer
	compressed, err := lastLayer.Compressed()
	if err != nil {
		return fmt.Errorf("failed to get compressed layer: %w", err)
	}
	defer compressed.Close()

	gr, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	// Save the uncompressed tar layer to a file
	out, err := os.Create(tarFileName)
	if err != nil {
		return fmt.Errorf("failed to create output tar file: %w", err)
	}
	defer os.Remove(tarFileName)
	defer out.Close()

	if _, err := io.Copy(out, gr); err != nil {
		return fmt.Errorf("failed to decompress layer: %w", err)
	}
	zap.L().Info("Layer written", zap.String("file_name", tarFileName))

	// Remove whiteout files from the tar archive and save it to a new file
	if err := removeWhiteouts(tarFileName, cleanedFileName); err != nil {
		return fmt.Errorf("failed to clean layer: %w", err)
	}
	zap.L().Info("Cleaned layer saved", zap.String("file_name", cleanedFileName))

	return nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// removeWhiteouts removes .wh.* files from a tar archive.
func removeWhiteouts(inputTar, outputTar string) error {
	in, err := os.Open(inputTar)
	if err != nil {
		return fmt.Errorf("open input tar: %w", err)
	}
	defer in.Close()

	tr := tar.NewReader(in)

	out, err := os.Create(outputTar)
	if err != nil {
		return fmt.Errorf("create output tar: %w", err)
	}
	defer out.Close()

	tw := tar.NewWriter(out)
	defer tw.Close()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		if isWhiteout(hdr.Name) {
			continue
		}

		// Clone header to avoid corrupting iteration
		hdrCopy := *hdr
		if err := tw.WriteHeader(&hdrCopy); err != nil {
			return fmt.Errorf("write tar header: %w", err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return fmt.Errorf("write tar content: %w", err)
		}
	}
	return nil
}

// isWhiteout returns true if the file is a whiteout file.
func isWhiteout(path string) bool {
	base := filepath.Base(path)
	return strings.HasPrefix(base, ".wh.")
}

func writeDockerfile(path string) error {
	return os.WriteFile(path, []byte(dockerfileContent), 0644)
}

func buildDockerImageTar(tag string, tarPath string) error {
	// Create a temporary Dockerfile
	dockerfilePath := "Dockerfile.temp"
	if err := writeDockerfile(dockerfilePath); err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}
	defer os.Remove(dockerfilePath)

	err := runCommand("docker", "build", "-t", tag, "-f", dockerfilePath, ".")
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}
	err = runCommand("docker", "save", "-o", tarPath, tag)
	if err != nil {
		return fmt.Errorf("failed to save image: %w", err)
	}
	return nil
}

func getLastLayerFromImage(img v1.Image) (v1.Layer, error) {
	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("failed to get image layers: %w", err)
	}
	if len(layers) == 0 {
		return nil, fmt.Errorf("no layers found in image")
	}
	return layers[len(layers)-1], nil
}
