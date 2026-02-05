package nfsproxy

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	nfsserver "github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/portmap"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	// setup logging
	logCfg := zap.NewDevelopmentConfig()
	logCfg.DisableStacktrace = true
	log, err := logCfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	require.NoError(t, err)
	zap.ReplaceGlobals(log)

	// setup data
	sandboxID := uuid.NewString()
	teamID := uuid.NewString()
	volPath1 := os.TempDir()
	volType1 := "volume-type-1"
	volName1 := "volume-1"
	volName2 := "volume-2"
	volType2 := "volume-type-2"

	slot := &network.Slot{Key: "abc", HostIP: net.IPv4(127, 0, 0, 1)}
	require.Equal(t, "127.0.0.1", slot.HostIP.String(), "required for the test to work")

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{
					{Name: volName1, Path: "/mnt/vol1", Type: volType1},
					{Name: volName2, Path: "/mnt/vol2", Type: volType2},
				},
			},
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
				TeamID:    teamID,
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	// setup nfs proxy server
	nfsConfig := net.ListenConfig{}
	nfsListener, err := nfsConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := nfsListener.Close()
		assert.NoError(t, err)
	})

	config := cfg.Config{
		PersistentVolumeMounts: map[string]string{
			volType1: volPath1,
		},
	}

	nfsProxy := NewProxy(t.Context(), sandboxes, config)
	go func() {
		err := nfsProxy.Serve(nfsListener)
		assert.NoError(t, err)
	}()

	// get nfs server's dynamic port
	nfsAddr := nfsListener.Addr().String()
	host, portText, err := net.SplitHostPort(nfsAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	// setup portmap server
	portmapConfig := net.ListenConfig{}
	pmListener, err := portmapConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := pmListener.Close()
		assert.NoError(t, err)
	})

	pm := portmap.NewPortMap(t.Context())
	pm.RegisterPort(t.Context(), uint32(port))
	go func() {
		err := pm.Serve(t.Context(), pmListener)
		assert.NoError(t, err)
	}()

	// connect via nfs client
	auth := rpc.NewAuthUnix("", 100, 101)

	// dial portmap server
	portmapperTCPClient, err := rpc.DialTCP("tcp", pmListener.Addr().String(), false)
	require.NoError(t, err)
	t.Cleanup(func() {
		portmapperTCPClient.Close()
	})
	portmapperClient := &rpc.Portmapper{Client: portmapperTCPClient}
	t.Cleanup(func() {
		portmapperClient.Close()
	})

	retrievedPort, err := portmapperClient.Getport(rpc.Mapping{
		Prog: nfs.Nfs3Prog,
		Vers: nfs.Nfs3Vers,
		Prot: rfc1057.IPPROTO_TCP,
	})
	require.NoError(t, err)

	nfsClient, err := nfs.DialServiceAtPort(host, retrievedPort)
	require.NoError(t, err)

	// request mount
	mount := &nfs.Mount{
		Client: nfsClient,
	}
	target, err := mount.Mount("/"+volName1, auth.Auth())
	require.NoError(t, err)

	t.Run("write file", func(t *testing.T) {
		t.Parallel()

		// write a file through nfs
		const perms os.FileMode = 0o642
		fp, err := target.OpenFile("/sandbox-id.txt", perms)
		require.NoError(t, err)
		data := []byte(sandboxID)
		n, err := fp.Write(data)
		require.NoError(t, err)
		assert.Equal(t, len(data), n)
		err = fp.Close()
		require.NoError(t, err)

		// verify file contents directly
		objectName := filepath.Join(volPath1, teamID, volName1, "sandbox-id.txt")
		object, err := os.Open(objectName)
		require.NoError(t, err)

		// verify metadata
		stat, err := object.Stat()
		require.NoError(t, err)
		assert.Equal(t, perms, stat.Mode().Perm(), "wrong permissions for %s", objectName)

		// verify contents
		data, err = io.ReadAll(object)
		require.NoError(t, err)
		assert.Equal(t, sandboxID, string(data))
	})

	t.Run("mkdir", func(t *testing.T) {
		t.Parallel()

		path := uuid.NewString()
		fh, err := target.Mkdir(path, 0o755)
		require.NoError(t, err)
		assert.NotNil(t, fh)
	})

	t.Run("list file in nfs", func(t *testing.T) {
		t.Parallel()

		// setup root dir, to prevent collisions
		path := uuid.NewString()
		mkdir(t, target, path, 0o755)

		// write files
		writeFile(t, target, filepath.Join(path, "file.txt"), "file.txt contents", 0o644)
		writeFile(t, target, filepath.Join(path, "file2.txt"), "file2.txt contents", 0o755)

		// ensure files can be listed
		items, err := target.ReadDirPlus(path)
		require.NoError(t, err)
		require.Len(t, items, 2)

		// normalize the order
		slices.SortFunc(items, func(a, b *nfs.EntryPlus) int {
			return strings.Compare(a.Name(), b.Name())
		})

		assert.Equal(t, "file.txt", items[0].Name())
		assert.Equal(t, os.FileMode(0o644), items[0].Mode())
		assert.Equal(t, "file2.txt", items[1].Name())
		assert.Equal(t, os.FileMode(0o755), items[1].Mode())
	})

	t.Run("access", func(t *testing.T) {
		t.Parallel()

		path := uuid.NewString()
		mkdir(t, target, path, 0o755)
		writeFile(t, target, filepath.Join(path, "file.txt"), "file.txt contents", 0o644)
		mode, err := target.Access(filepath.Join(path, "file.txt"), 0o644)
		require.NoError(t, err)
		assert.Equal(t, uint32(0o644), mode)
	})

	t.Run("lookup missing file", func(t *testing.T) {
		t.Parallel()

		// verify that file can be read with getattr
		path := uuid.NewString()
		stat1, fh1, err := target.Lookup(path)
		require.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, fh1)
		assert.Nil(t, stat1)
	})
}

func writeFile(t *testing.T, target *nfs.Target, path string, content string, perm os.FileMode) {
	t.Helper()

	fp, err := target.OpenFile(path, perm)
	require.NoError(t, err)

	n, err := fp.Write([]byte(content))
	require.NoError(t, err)
	assert.Equal(t, len(content), n, "wrong number of bytes written")

	err = fp.Close()
	require.NoError(t, err)
}

func mkdir(t *testing.T, target *nfs.Target, path string, perm os.FileMode) []byte {
	t.Helper()

	fh, err := target.Mkdir(path, perm)
	require.NoError(t, err)

	return fh
}

func TestGetPrefixFromSandbox(t *testing.T) {
	t.Parallel()

	sandboxes := sandbox.NewSandboxesMap()

	// happy path variables
	happyIP := net.IPv4(127, 0, 0, 1)
	happyAddr := &net.TCPAddr{
		IP:   happyIP,
		Port: 12345,
	}
	happyDirPath := "/good-volume"
	happyFS := memfs.New()
	happyPrefix := filepath.Join("team-id", "good-volume")
	happyVolumeName := "good-volume"
	happyVolumeType := "good-volume-type"

	happySlot := &network.Slot{Key: "abc", HostIP: happyIP}
	happySandbox := &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{
					{Name: happyVolumeName, Path: "/volume", Type: happyVolumeType},
				},
			},
			Runtime: sandbox.RuntimeMetadata{
				TeamID: "team-id",
			},
		},
		Resources: &sandbox.Resources{
			Slot: happySlot,
		},
	}

	sandboxes.Insert(happySandbox)

	filesystemsByType := map[string]billy.Filesystem{
		happyVolumeType: happyFS,
	}

	type expectations struct {
		fs     billy.Filesystem
		prefix string
		err    error
	}

	testCases := map[string]struct {
		remoteAddr net.Addr
		dirpath    string
		expected   expectations
	}{
		"happy path": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath,
			expected: expectations{
				fs:     happyFS,
				prefix: happyPrefix,
				err:    nil,
			},
		},
		"happy path with subfolder": {
			remoteAddr: happyAddr,
			dirpath:    filepath.Join(happyDirPath, "subfolder"),
			expected: expectations{
				fs:     happyFS,
				prefix: filepath.Join(happyPrefix, "subfolder"),
			},
		},
		"cannot mount dot": {
			remoteAddr: happyAddr,
			dirpath:    ".",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrMustMountAbsolutePath,
			},
		},
		"cannot mount relative path": {
			remoteAddr: happyAddr,
			dirpath:    "good-volume",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrMustMountAbsolutePath,
			},
		},
		"cannot mount root": {
			remoteAddr: happyAddr,
			dirpath:    "/",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
		"volume not found": {
			remoteAddr: happyAddr,
			dirpath:    "/nonexistent-volume",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    fmt.Errorf("failed to mount %q: %w", "nonexistent-volume", ErrVolumeNotFound),
			},
		},
		"sandbox not found": {
			remoteAddr: &net.TCPAddr{IP: net.IPv4(1, 2, 3, 4), Port: 12345},
			dirpath:    happyDirPath,
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    fmt.Errorf("sandbox with address 1.2.3.4:12345 not found"),
			},
		},
		"volume type not supported": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath,
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    fmt.Errorf("failed to mount %q (%s): %w", happyVolumeName, happyVolumeType, ErrVolumeTypeNotSupported),
			},
		},
		"volume name is empty (trailing slash)": {
			remoteAddr: happyAddr,
			dirpath:    "//",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
		"multiple path segments": {
			remoteAddr: happyAddr,
			dirpath:    filepath.Join(happyDirPath, "sub1", "sub2"),
			expected: expectations{
				fs:     happyFS,
				prefix: filepath.Join(happyPrefix, "sub1", "sub2"),
			},
		},
		"path with trailing slash": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath + "/",
			expected: expectations{
				fs:     happyFS,
				prefix: happyPrefix,
			},
		},
		"path with multiple leading slashes": {
			remoteAddr: happyAddr,
			dirpath:    "///" + happyDirPath,
			expected: expectations{
				fs:     happyFS,
				prefix: happyPrefix,
			},
		},
		"chroot escape attempt: ..happy": {
			remoteAddr: happyAddr,
			dirpath:    "/../" + happyDirPath,
			expected: expectations{
				fs:     happyFS,
				prefix: happyPrefix,
			},
		},
		"chroot escape attempt: ....happy": {
			remoteAddr: happyAddr,
			dirpath:    "/../../" + happyDirPath,
			expected: expectations{
				fs:     happyFS,
				prefix: happyPrefix,
			},
		},
		"chroot escape attempt: ..": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath + "/..",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
		"chroot escape attempt: .. with trailing slash": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath + "/../",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
		"chroot escape attempt: ....": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath + "/../..",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
		"chroot escape attempt: .... with trailing": {
			remoteAddr: happyAddr,
			dirpath:    happyDirPath + "/../../",
			expected: expectations{
				fs:     nil,
				prefix: "",
				err:    ErrCannotMountRoot,
			},
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := nfsserver.MountRequest{
				Dirpath: []byte(tc.dirpath),
			}

			fsByType := filesystemsByType
			if name == "volume type not supported" {
				fsByType = nil
			}

			handler := getPrefixFromSandbox(sandboxes, fsByType)

			fs, prefix, err := handler(t.Context(), tc.remoteAddr, request)
			assert.Equal(t, tc.expected.fs, fs)
			assert.Equal(t, tc.expected.prefix, prefix)
			if tc.expected.err != nil {
				assert.EqualError(t, err, tc.expected.err.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
