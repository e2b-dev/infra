//go:build linux

package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/config"
)

func TestAdditionalOCILayers(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()

		envdPath := tempDir + "/envd"
		err := os.WriteFile(envdPath, []byte("echo hello"), 0o755)
		require.NoError(t, err)

		busyboxVersion := "1.36.1"
		busyboxDir := tempDir + "/busybox"
		err = os.MkdirAll(filepath.Join(busyboxDir, busyboxVersion, runtime.GOARCH), 0o755)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(busyboxDir, busyboxVersion, runtime.GOARCH, "busybox"), []byte("busybox-binary"), 0o755)
		require.NoError(t, err)

		buildContext := buildcontext.BuildContext{
			BuilderConfig: cfg.BuilderConfig{
				HostEnvdPath:   envdPath,
				HostBusyboxDir: busyboxDir,
				BusyboxVersion: busyboxVersion,
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
		assert.Len(t, keys, 14)
		assert.Equal(t, "e2b.local", actualFiles["etc/hostname"])
		assert.Equal(t, "nameserver 8.8.8.8", actualFiles["etc/resolv.conf"])

		// verify that memory function works
		assert.Contains(t, actualFiles["etc/systemd/system/envd.service"], `"GOMEMLIMIT=50MiB"`)

		// verify that systemd is configured to retry envd forever
		assert.Contains(t, actualFiles["etc/systemd/system/envd.service"], "StartLimitIntervalSec=0")

		// Regression guard: envd must be ordered after systemd-tmpfiles-setup.service.
		// updateEnvd stages its replacement binary in /tmp during early boot, and on
		// our Ubuntu/Debian base images systemd-tmpfiles-setup.service wipes /tmp's
		// contents at boot (`D /tmp` rule run with --remove). Without this ordering
		// envd can answer the build's upload before the wipe, and the staged
		// /tmp/envd_updated is deleted, so the follow-up chmod/mv fails with ENOENT.
		envdAfter := ""
		for line := range strings.SplitSeq(actualFiles["etc/systemd/system/envd.service"], "\n") {
			if strings.HasPrefix(line, "After=") {
				envdAfter = line

				break
			}
		}
		require.NotEmpty(t, envdAfter, "envd.service must declare an After= ordering")
		assert.Contains(t, envdAfter, "systemd-tmpfiles-setup.service",
			"envd.service After= must order envd after the boot-time /tmp wipe")

		// ensure that both files have identical content
		disabledContent := strings.TrimSpace(`
[Service]
WatchdogSec=0`)
		assert.Equal(t, disabledContent, actualFiles["etc/systemd/system/systemd-journald.service.d/override.conf"])
		assert.Equal(t, disabledContent, actualFiles["etc/systemd/system/systemd-networkd.service.d/override.conf"])
	})
}
