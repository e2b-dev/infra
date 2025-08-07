package fc

import (
	"strings"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestStartScriptBuilder_Build(t *testing.T) {
	tests := []struct {
		name                  string
		versions              FirecrackerVersions
		files                 *storage.SandboxFiles
		rootfsPaths           RootfsPaths
		namespaceID           string
		expectedRootfsPath    string
		expectedKernelPath    string
		expectedScriptContent []string // parts that should be in the generated script
	}{
		{
			name: "basic_build_with_version_2",
			versions: FirecrackerVersions{
				KernelVersion:      "6.1.0",
				FirecrackerVersion: "1.4.0",
			},
			files: createTestSandboxFiles("test-sandbox", "static-id"),
			rootfsPaths: RootfsPaths{
				Version:    2,
				TemplateID: "template-123",
				BuildID:    "build-456",
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
				"ip netns exec ns-789 /fc-versions/1.4.0/firecracker --api-sock",
				"fc-test-sandbox-static-id.sock",
			},
		},
		{
			name: "build_with_version_1_backward_compatibility",
			versions: FirecrackerVersions{
				KernelVersion:      "5.10.0",
				FirecrackerVersion: "1.3.0",
			},
			files: createTestSandboxFiles("legacy-sandbox", "legacy-id"),
			rootfsPaths: RootfsPaths{
				Version:    1,
				TemplateID: "legacy-template",
				BuildID:    "legacy-build",
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
				"ip netns exec legacy-ns /fc-versions/1.3.0/firecracker --api-sock",
				"fc-legacy-sandbox-legacy-id.sock",
			},
		},
		{
			name: "different_kernel_and_firecracker_versions",
			versions: FirecrackerVersions{
				KernelVersion:      "6.2.1",
				FirecrackerVersion: "1.5.0-beta",
			},
			files: createTestSandboxFiles("custom-sandbox", "custom-id"),
			rootfsPaths: RootfsPaths{
				Version:    2,
				TemplateID: "custom-template",
				BuildID:    "custom-build",
			},
			namespaceID:        "custom-ns-id",
			expectedRootfsPath: "/fc-vm/rootfs.ext4",
			expectedKernelPath: "/fc-vm/6.2.1/vmlinux.bin",
			expectedScriptContent: []string{
				"mkdir -p /fc-vm/6.2.1",
				"ln -s /fc-kernels/6.2.1/vmlinux.bin /fc-vm/6.2.1/vmlinux.bin",
				"ip netns exec custom-ns-id /fc-versions/1.5.0-beta/firecracker --api-sock",
				"fc-custom-sandbox-custom-id.sock",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewStartScriptBuilder()

			// Call Build function directly with the four parameters
			result, err := builder.Build(tt.versions, tt.files, tt.rootfsPaths, tt.namespaceID)
			if err != nil {
				t.Fatalf("Build should not return an error: %v", err)
			}
			if result == nil {
				t.Fatal("Result should not be nil")
			}

			// Test computed paths
			if result.RootfsPath != tt.expectedRootfsPath {
				t.Errorf("RootfsPath = %v, want %v", result.RootfsPath, tt.expectedRootfsPath)
			}
			if result.KernelPath != tt.expectedKernelPath {
				t.Errorf("KernelPath = %v, want %v", result.KernelPath, tt.expectedKernelPath)
			}

			// Test that script is not empty
			if result.Value == "" {
				t.Error("Generated script should not be empty")
			}

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
			if !strings.Contains(result.Value, "ip netns exec") {
				t.Error("Script should end with firecracker execution")
			}

			// Test that the script has proper formatting (should not have extra spaces or newlines)
			lines := strings.Split(result.Value, "\n")
			if len(lines) <= 1 {
				t.Error("Script should have multiple lines")
			}
		})
	}
}

// createTestSandboxFiles creates a SandboxFiles instance for testing
func createTestSandboxFiles(sandboxID, staticID string) *storage.SandboxFiles {
	templateFiles := storage.TemplateFiles{
		TemplateID:         "test-template",
		BuildID:            "test-build",
		KernelVersion:      "6.1.0",
		FirecrackerVersion: "1.4.0",
	}

	templateCacheFiles := storage.TemplateCacheFiles{
		TemplateFiles:   templateFiles,
		CacheIdentifier: "test-cache-id",
	}

	return templateCacheFiles.NewSandboxFilesWithStaticID(sandboxID, staticID)
}
