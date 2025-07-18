package oci

import (
	"archive/tar"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
)

func createFileTar(t *testing.T, fileName string) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Add a file to the tarball
	content := []byte("layer text")
	err := tw.WriteHeader(&tar.Header{
		Name: fileName + ".txt",
		Mode: 0o600,
		Size: int64(len(content)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	return &buf
}

func TestCreateExportLayersOrder(t *testing.T) {
	ctx := t.Context()

	tracer := noop.NewTracerProvider().Tracer("test")
	postProcessor := writer.NewPostProcessor(ctx, zap.NewNop(), false)

	// Create a dummy image with some layers
	img := empty.Image
	layer1, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(createFileTar(t, "layer0").Bytes())), nil
	})
	require.NoError(t, err)
	img, err = mutate.AppendLayers(img, layer1)
	require.NoError(t, err)

	layer2, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(createFileTar(t, "layer1").Bytes())), nil
	})
	require.NoError(t, err)
	img, err = mutate.AppendLayers(img, layer2)
	require.NoError(t, err)

	layer3, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(createFileTar(t, "layer2").Bytes())), nil
	})
	require.NoError(t, err)
	img, err = mutate.AppendLayers(img, layer3)
	require.NoError(t, err)

	// Export the layers
	dir := t.TempDir()
	layers, err := createExport(ctx, tracer, postProcessor, img, dir)
	require.NoError(t, err)
	require.NotNil(t, layers)

	// Layers should be in reverse order
	assert.Equal(t, 3, len(layers))
	assert.Regexp(t, "/layer-2.*", strings.TrimPrefix(layers[0], dir))
	assert.FileExists(t, filepath.Join(layers[0], "layer2.txt"))
	assert.Regexp(t, "/layer-1.*", strings.TrimPrefix(layers[1], dir))
	assert.FileExists(t, filepath.Join(layers[1], "layer1.txt"))
	assert.Regexp(t, "/layer-0.*", strings.TrimPrefix(layers[2], dir))
	assert.FileExists(t, filepath.Join(layers[2], "layer0.txt"))
}
