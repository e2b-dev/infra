package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
)

func TestAdditionalOCILayers(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		envdPath := tempDir + "/envd"
		err := os.WriteFile(envdPath, []byte("echo hello"), 0o755)
		require.NoError(t, err)

		buildContext := buildcontext.BuildContext{
			BuilderConfig: cfg.BuilderConfig{
				HostEnvdPath: envdPath,
			},
			Config: config.TemplateConfig{
				MemoryMB: 100,
			},
		}
		provisionScript := "provision.sh"
		provisionLogPrefix := "provision.log"
		provisionResultPath := "provision.result"

		layers, err := additionalOCILayers(buildContext, provisionScript, provisionLogPrefix, provisionResultPath)
		require.NoError(t, err)

		require.Len(t, layers, 2)
		layer1 := layers[0]
		filesLayer, err := layer1.Uncompressed()
		require.NoError(t, err)
		t.Cleanup(func() {
			err = filesLayer.Close()
			assert.NoError(t, err)
		})

		actualFiles := map[string]string{}
		filesTarReader := tar.NewReader(filesLayer)
		for {
			header, err := filesTarReader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)

			if header.Typeflag != tar.TypeReg {
				// we're only verifying files for now
				continue
			}

			filename := header.Name
			var buffer bytes.Buffer
			count, err := io.CopyN(&buffer, filesTarReader, header.Size)
			require.NoError(t, err)
			assert.Equal(t, header.Size, count)
			actualFiles[filename] = buffer.String()
		}

		keysIter := maps.Keys(actualFiles)
		keys := slices.Collect(keysIter)
		assert.Len(t, keys, 13)
		assert.Equal(t, "e2b.local", actualFiles["etc/hostname"])
		assert.Equal(t, "nameserver 8.8.8.8", actualFiles["etc/resolv.conf"])

		// verify that memory function works
		assert.Contains(t, actualFiles["etc/systemd/system/envd.service"], `"GOMEMLIMIT=50MiB"`)

		// ensure that both files have identical content
		disabledContent := strings.TrimSpace(`
[Service]
WatchdogSec=0`)
		assert.Equal(t, disabledContent, actualFiles["etc/systemd/system/systemd-journald.service.d/override.conf"])
		assert.Equal(t, disabledContent, actualFiles["etc/systemd/system/systemd-networkd.service.d/override.conf"])
	})
}
