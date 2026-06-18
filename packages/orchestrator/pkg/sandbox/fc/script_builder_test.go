//go:build linux

package fc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestStartScriptBuilder_Build(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	tests := []struct {
		name                  string
		versions              Config
		files                 *storage.SandboxFiles
		rootfsPaths           RootfsPaths
		namespaceID           string
		expectedRootfsPath    string
		expectedKernelPath    string
		expectedScriptContent []string // parts that should be in the generated script
	}{
		{
			name: "basic_build_with_version_2",
			versions: Config{
				KernelVersion:      "6.1.0",
				FirecrackerVersion: "1.4.0",
			},
			files: createTestSandboxFiles("test-sandbox", "static-id"),
			rootfsPaths: RootfsPaths{
				TemplateVersion: 2,
				TemplateID:      "template-123",
				BuildID:         "build-456",
			},
			namespaceID:        "ns-789",
			expectedRootfsPath: "/fc-vm/rootfs.ext4",
			expectedKernelPath: "/fc-vm/6.1.0/vmlinux.bin",
			expectedScriptContent: []string{
				"mount --make-rprivate /",
				"mount -t tmpfs tmpfs /fc-vm -o X-mount.mkdir",
				"ln -s /orchestrator/sandbox/rootfs-test-sandbox-static-id.link /fc-vm/rootfs.ext4",
				"mkdir -p /fc-vm/6.1.0",
				"ln -s /fc-kernels/6.1.0/vmlinux.bin /fc-vm/6.1.0/vmlinux.bin",
				"nsenter --net=/var/run/netns/ns-789 -- /fc-versions/1.4.0/firecracker --api-sock",
				"fc-test-sandbox-static-id.sock",
			},
		},
		{
			name: "build_with_version_1_backward_compatibility",
			versions: Config{
				KernelVersion:      "5.10.0",
				FirecrackerVersion: "1.3.0",
			},
			files: createTestSandboxFiles("legacy-sandbox", "legacy-id"),
			rootfsPaths: RootfsPaths{
				TemplateVersion: 1,
				TemplateID:      "legacy-template",
				BuildID:         "legacy-build",
			},
			namespaceID:        "legacy-ns",
			expectedRootfsPath: "/mnt/disks/fc-envs/v1/legacy-template/builds/legacy-build/rootfs.ext4",
			expectedKernelPath: "/fc-vm/5.10.0/vmlinux.bin",
			expectedScriptContent: []string{
				"mount --make-rprivate /",
				"mount -t tmpfs tmpfs /mnt/disks/fc-envs/v1/legacy-template/builds/legacy-build -o X-mount.mkdir",
				"ln -s /orchestrator/sandbox/rootfs-legacy-sandbox-legacy-id.link /mnt/disks/fc-envs/v1/legacy-template/builds/legacy-build/rootfs.ext4",
				"mount -t tmpfs tmpfs /fc-vm/5.10.0 -o X-mount.mkdir",
				"ln -s /fc-kernels/5.10.0/vmlinux.bin /fc-vm/5.10.0/vmlinux.bin",
				"nsenter --net=/var/run/netns/legacy-ns -- /fc-versions/1.3.0/firecracker --api-sock",
				"fc-legacy-sandbox-legacy-id.sock",
			},
		},
		{
			name: "different_kernel_and_firecracker_versions",
			versions: Config{
				KernelVersion:      "6.2.1",
				FirecrackerVersion: "1.5.0-beta",
			},
			files: createTestSandboxFiles("custom-sandbox", "custom-id"),
			rootfsPaths: RootfsPaths{
				TemplateVersion: 2,
				TemplateID:      "custom-template",
				BuildID:         "custom-build",
			},
			namespaceID:        "custom-ns-id",
			expectedRootfsPath: "/fc-vm/rootfs.ext4",
			expectedKernelPath: "/fc-vm/6.2.1/vmlinux.bin",
			expectedScriptContent: []string{
				"mkdir -p /fc-vm/6.2.1",
				"ln -s /fc-kernels/6.2.1/vmlinux.bin /fc-vm/6.2.1/vmlinux.bin",
				"nsenter --net=/var/run/netns/custom-ns-id -- /fc-versions/1.5.0-beta/firecracker --api-sock",
				"fc-custom-sandbox-custom-id.sock",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder := NewStartScriptBuilder(config)

			// Call Build function directly with the four parameters
			result, err := builder.Build(tt.versions, tt.files, tt.rootfsPaths, tt.namespaceID)
			require.NoError(t, err)
			require.NotNil(t, result)

			// Test computed paths
			assert.Equal(t, tt.expectedRootfsPath, result.RootfsPath)
			assert.Equal(t, tt.expectedKernelPath, result.KernelPath)

			// Test that script is not empty
			assert.NotEmpty(t, result.Value)

			// Test that script contains expected content
			for _, expected := range tt.expectedScriptContent {
				if !strings.Contains(result.Value, expected) {
					t.Errorf("Script should contain expected content: %s\nActual script:\n%s", expected, result.Value)
				}
			}

			// Test script structure - should have all major sections
			if !strings.Contains(result.Value, "mount --make-rprivate /") {
				t.Error("Script should start with mount command")
			}
			if !strings.Contains(result.Value, "nsenter --net=") {
				t.Error("Script should end with firecracker execution via nsenter")
			}

			// Test that the script has proper formatting (should not have extra spaces or newlines)
			lines := strings.Split(result.Value, "\n")
			if len(lines) <= 1 {
				t.Error("Script should have multiple lines")
			}
		})
	}
}

// TestStartScriptBuilder_NoIpNetnsExec 验证优化后脚本中不再包含旧的 ip netns exec 命令。
// 这是性能优化的核心保证：ip netns exec 内部会额外调用 unshare(CLONE_NEWNS)，
// 导致第二次挂载树拷贝，在高并发下争抢 namespace_sem 全局内核锁。
func TestStartScriptBuilder_NoIpNetnsExec(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	builder := NewStartScriptBuilder(config)

	for _, version := range []uint64{1, 2} {
		version := version
		t.Run(fmt.Sprintf("template_v%d", version), func(t *testing.T) {
			t.Parallel()
			result, err := builder.Build(
				Config{KernelVersion: "6.1.0", FirecrackerVersion: "1.4.0"},
				createTestSandboxFiles("sbx", "sid"),
				RootfsPaths{TemplateVersion: version, TemplateID: "tmpl", BuildID: "bld"},
				"ns-42",
			)
			require.NoError(t, err)

			// 核心断言：旧命令必须完全消失
			assert.NotContains(t, result.Value, "ip netns exec",
				"脚本不应再包含 ip netns exec（会触发第二次 mount namespace 拷贝）")
		})
	}
}

// TestStartScriptBuilder_NsenterFormat 验证 nsenter 命令格式的正确性：
//  1. 必须使用完整路径 /var/run/netns/<id>
//  2. 必须包含 -- 分隔符，防止 firecracker 参数被 nsenter 误解析
//  3. nsenter 必须紧接 firecracker 二进制路径
func TestStartScriptBuilder_NsenterFormat(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	builder := NewStartScriptBuilder(config)

	tests := []struct {
		name        string
		namespaceID string
		wantNetPath string
	}{
		{"numeric_slot", "ns-1", "/var/run/netns/ns-1"},
		{"numeric_slot_large", "ns-32766", "/var/run/netns/ns-32766"},
		{"custom_name", "my-custom-ns", "/var/run/netns/my-custom-ns"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := builder.Build(
				Config{KernelVersion: "6.1.0", FirecrackerVersion: "1.4.0"},
				createTestSandboxFiles("sbx", "sid"),
				RootfsPaths{TemplateVersion: 2, TemplateID: "tmpl", BuildID: "bld"},
				tt.namespaceID,
			)
			require.NoError(t, err)

			// 验证完整路径格式
			assert.Contains(t, result.Value, fmt.Sprintf("nsenter --net=%s --", tt.wantNetPath),
				"nsenter 必须使用 /var/run/netns/ 前缀的完整路径，并带 -- 分隔符")

			// 验证 -- 分隔符存在（防止 firecracker 参数被 nsenter 吞掉）
			assert.Contains(t, result.Value, "nsenter --net="+tt.wantNetPath+" --",
				"nsenter 和 firecracker 之间必须有 -- 分隔符")
		})
	}
}

// TestStartScriptBuilder_NetNamespacePath 直接验证 buildArgs 中 NetNamespacePath 的路径拼接逻辑。
// filepath.Join("/var/run/netns", namespaceID) 必须产生正确路径，
// 不能出现双斜杠或路径截断。
func TestStartScriptBuilder_NetNamespacePath(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	builder := NewStartScriptBuilder(config)

	cases := []struct {
		namespaceID  string
		expectedPath string
	}{
		{"ns-1", "/var/run/netns/ns-1"},
		{"ns-100", "/var/run/netns/ns-100"},
	}

	for _, c := range cases {
		c := c
		t.Run(c.namespaceID, func(t *testing.T) {
			t.Parallel()
			args := builder.buildArgs(
				Config{KernelVersion: "6.1.0", FirecrackerVersion: "1.4.0"},
				createTestSandboxFiles("sbx", "sid"),
				RootfsPaths{TemplateVersion: 2, TemplateID: "tmpl", BuildID: "bld"},
				c.namespaceID,
			)
			assert.Equal(t, c.expectedPath, args.NetNamespacePath,
				"NetNamespacePath 必须是 /var/run/netns/<namespaceID>")
		})
	}
}

// TestStartScriptBuilder_TemplateVersionBoundary 验证模板版本边界：
// TemplateVersion <= 1 使用 V1 模板（兼容旧路径），>= 2 使用 V2 模板。
// 两个模板都必须使用 nsenter 而非 ip netns exec。
func TestStartScriptBuilder_TemplateVersionBoundary(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	builder := NewStartScriptBuilder(config)

	// TemplateVersion=0 应走 V1 分支（<= 1）
	t.Run("version_0_uses_v1_template", func(t *testing.T) {
		t.Parallel()
		result, err := builder.Build(
			Config{KernelVersion: "5.10.0", FirecrackerVersion: "1.3.0"},
			createTestSandboxFiles("sbx", "sid"),
			RootfsPaths{TemplateVersion: 0, TemplateID: "tmpl", BuildID: "bld"},
			"ns-5",
		)
		require.NoError(t, err)
		// V1 模板挂载 DeprecatedSandboxRootfsDir
		assert.Contains(t, result.Value, "mount -t tmpfs tmpfs /mnt/disks/fc-envs/v1/tmpl/builds/bld",
			"TemplateVersion=0 应使用 V1 模板（旧路径）")
		assert.Contains(t, result.Value, "nsenter --net=/var/run/netns/ns-5 --",
			"V1 模板也必须使用 nsenter")
		assert.NotContains(t, result.Value, "ip netns exec")
	})

	// TemplateVersion=1 应走 V1 分支
	t.Run("version_1_uses_v1_template", func(t *testing.T) {
		t.Parallel()
		result, err := builder.Build(
			Config{KernelVersion: "5.10.0", FirecrackerVersion: "1.3.0"},
			createTestSandboxFiles("sbx", "sid"),
			RootfsPaths{TemplateVersion: 1, TemplateID: "tmpl", BuildID: "bld"},
			"ns-5",
		)
		require.NoError(t, err)
		assert.Contains(t, result.Value, "nsenter --net=/var/run/netns/ns-5 --")
		assert.NotContains(t, result.Value, "ip netns exec")
	})

	// TemplateVersion=2 应走 V2 分支
	t.Run("version_2_uses_v2_template", func(t *testing.T) {
		t.Parallel()
		result, err := builder.Build(
			Config{KernelVersion: "6.1.0", FirecrackerVersion: "1.4.0"},
			createTestSandboxFiles("sbx", "sid"),
			RootfsPaths{TemplateVersion: 2, TemplateID: "tmpl", BuildID: "bld"},
			"ns-5",
		)
		require.NoError(t, err)
		// V2 模板直接挂载 SandboxDir
		assert.Contains(t, result.Value, "mount -t tmpfs tmpfs /fc-vm -o X-mount.mkdir",
			"TemplateVersion=2 应使用 V2 模板（新路径）")
		assert.Contains(t, result.Value, "nsenter --net=/var/run/netns/ns-5 --")
		assert.NotContains(t, result.Value, "ip netns exec")
	})
}

// TestStartScriptBuilder_ScriptStructureOrder 验证脚本命令的执行顺序：
// 必须先完成所有挂载操作，最后才执行 nsenter 进入网络命名空间启动 firecracker。
// 顺序错误会导致 firecracker 在挂载完成前启动，找不到 rootfs/kernel 文件。
func TestStartScriptBuilder_ScriptStructureOrder(t *testing.T) {
	t.Parallel()
	config, err := cfg.ParseBuilder()
	require.NoError(t, err)

	builder := NewStartScriptBuilder(config)

	result, err := builder.Build(
		Config{KernelVersion: "6.1.0", FirecrackerVersion: "1.4.0"},
		createTestSandboxFiles("sbx", "sid"),
		RootfsPaths{TemplateVersion: 2, TemplateID: "tmpl", BuildID: "bld"},
		"ns-10",
	)
	require.NoError(t, err)

	mountPos := strings.Index(result.Value, "mount --make-rprivate /")
	nsenterPos := strings.Index(result.Value, "nsenter --net=")

	assert.Greater(t, mountPos, -1, "脚本必须包含 mount --make-rprivate /")
	assert.Greater(t, nsenterPos, -1, "脚本必须包含 nsenter --net=")
	assert.Greater(t, nsenterPos, mountPos,
		"nsenter 必须在所有 mount 操作之后执行，确保挂载点就绪后再启动 firecracker")

	// nsenter 必须是脚本的最后一条命令（之后不应有其他 && 链接的命令）
	nsenterLine := result.Value[nsenterPos:]
	assert.NotContains(t, nsenterLine, " &&\n",
		"nsenter 启动 firecracker 后不应再有其他命令")
}

// createTestSandboxFiles creates a SandboxFiles instance for testing
func createTestSandboxFiles(sandboxID, staticID string) *storage.SandboxFiles {
	paths := storage.Paths{
		BuildID: "test-build",
	}

	cachePaths := storage.CachePaths{
		Paths:           paths,
		CacheIdentifier: "test-cache-id",
	}

	return cachePaths.NewSandboxFilesWithStaticID(sandboxID, staticID)
}
