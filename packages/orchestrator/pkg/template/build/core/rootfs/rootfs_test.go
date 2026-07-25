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
		assert.Len(t, keys, 18)

		// The provisioning boot must be self-contained on the baked busybox:
		// bare images (premade NixOS, distroless) have no /bin/sh, and
		// busybox init hands any inittab line with shell metacharacters to
		// /bin/sh — so the pipeline lives in the runner script and every
		// inittab entry is a plain exec.
		inittab := actualFiles["etc/inittab"]
		require.NotEmpty(t, inittab)
		for _, line := range strings.Split(inittab, "\n") {
			if !strings.HasPrefix(line, "::") {
				continue
			}
			assert.NotContains(t, line, "|", "inittab entries must not need /bin/sh: %s", line)
			assert.NotContains(t, line, "$", "inittab entries must not need /bin/sh: %s", line)
		}
		runner := actualFiles["usr/local/bin/e2b-provision-runner"]
		require.NotEmpty(t, runner, "provision runner must be baked")
		assert.Contains(t, runner, "#!/usr/bin/busybox ash")

		// Both init families' envd services seed certs via the shared script.
		seedCerts := actualFiles["usr/local/bin/e2b-seed-certs"]
		require.NotEmpty(t, seedCerts, "cert seeding script must be baked")
		assert.Contains(t, actualFiles["etc/systemd/system/envd.service"], "ExecStartPre=/usr/local/bin/e2b-seed-certs")

		// envd must be preset-enabled: first boot (machine-id is removed by
		// provisioning) applies the distro preset policy, and the RHEL
		// family's "disable *" would otherwise delete envd's autostart link.
		assert.Equal(t, "enable envd.service\n", actualFiles["etc/systemd/system-preset/00-e2b.preset"])

		// The OpenRC counterpart (Alpine, IMPL-145 W5) ships alongside the
		// systemd unit; it must supervise envd and honor the memory limit.
		// It lives OUTSIDE /etc/init.d — Debian's update-rc.d aborts on a
		// non-LSB script there — and is installed by the OpenRC init setup.
		openrcEnvd := actualFiles["usr/local/share/e2b/envd.openrc"]
		require.NotEmpty(t, openrcEnvd, "OpenRC envd service must be baked")
		assert.Contains(t, openrcEnvd, "#!/sbin/openrc-run")
		assert.Contains(t, openrcEnvd, "supervisor=supervise-daemon")
		assert.Contains(t, openrcEnvd, "GOMEMLIMIT=50MiB")
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

		// Regression guard (FEAT-145): the envd autostart symlink must not dangle.
		// A relative target resolves inside multi-user.target.wants/ and dangles,
		// and provision.sh's offline `systemctl enable` prunes dangling .wants
		// links — silently disabling envd autostart on e.g. Fedora.
		symlinksLayer, err := layers[1].Uncompressed()
		require.NoError(t, err)
		t.Cleanup(func() {
			err = symlinksLayer.Close()
			assert.NoError(t, err)
		})

		actualSymlinks := map[string]string{}
		symlinksTarReader := tar.NewReader(symlinksLayer)
		for {
			header, err := symlinksTarReader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(t, err)

			if header.Typeflag != tar.TypeSymlink {
				continue
			}
			actualSymlinks[header.Name] = header.Linkname
		}

		envdWants := actualSymlinks["etc/systemd/system/multi-user.target.wants/envd.service"]
		require.NotEmpty(t, envdWants, "envd autostart symlink must be present")
		assert.Equal(t, "/etc/systemd/system/envd.service", envdWants,
			"envd autostart symlink target must be absolute so it never dangles")
	})
}
