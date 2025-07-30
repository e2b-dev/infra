package mount

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// MountInfo tracks information about an active mount
type MountInfo struct {
	TemplateID   string
	BuildID      string
	MountPath    string
	DevicePath   string
	DeviceSlot   uint32
	TempFilePath string
}

// Manager manages template mounts and provides proper cleanup
type Manager struct {
	logger          *zap.Logger
	templateStorage storage.StorageProvider
	devicePool      *nbd.DevicePool
	
	// Track active mounts for cleanup
	activeMounts map[string]*MountInfo
	mu           sync.RWMutex
}

// NewManager creates a new mount manager
func NewManager(
	logger *zap.Logger,
	templateStorage storage.StorageProvider,
	devicePool *nbd.DevicePool,
) *Manager {
	return &Manager{
		logger:          logger,
		templateStorage: templateStorage,
		devicePool:      devicePool,
		activeMounts:    make(map[string]*MountInfo),
	}
}

// MountTemplate mounts a template to the specified path
func (m *Manager) MountTemplate(ctx context.Context, templateID, buildID, mountPath string) (*MountInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already mounted
	if existing, exists := m.activeMounts[mountPath]; exists {
		return existing, fmt.Errorf("path %s is already mounted with template %s/%s", mountPath, existing.TemplateID, existing.BuildID)
	}

	// Validate mount path
	if !filepath.IsAbs(mountPath) {
		return nil, fmt.Errorf("mount path must be absolute: %s", mountPath)
	}

	// Create mount directory if it doesn't exist
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create mount directory %s: %w", mountPath, err)
	}

	// Create template files metadata
	templateFiles := storage.TemplateFiles{
		TemplateID: templateID,
		BuildID:    buildID,
	}

	// Get the template rootfs from storage
	rootfsObject, err := m.templateStorage.OpenObject(ctx, templateFiles.StorageRootfsPath())
	if err != nil {
		return nil, fmt.Errorf("failed to open template rootfs from storage: %w", err)
	}

	// Create a temporary file to store the rootfs locally
	tempRootfsPath := filepath.Join("/tmp", fmt.Sprintf("template-%s-%s.ext4", templateID, buildID))

	// Download the rootfs to the temporary file
	tempFile, err := os.Create(tempRootfsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tempFile.Close()

	if _, err := rootfsObject.WriteTo(tempFile); err != nil {
		os.Remove(tempRootfsPath)
		return nil, fmt.Errorf("failed to download rootfs to temporary file: %w", err)
	}

	// Get an NBD device from the pool
	deviceSlot, err := m.devicePool.GetDevice(ctx)
	if err != nil {
		os.Remove(tempRootfsPath)
		return nil, fmt.Errorf("failed to get NBD device from pool: %w", err)
	}

	devicePath := nbd.GetDevicePath(deviceSlot)

	// Setup NBD connection to the rootfs file
	if err := m.setupNBDDevice(tempRootfsPath, devicePath); err != nil {
		// Clean up on error
		m.devicePool.ReleaseDevice(deviceSlot)
		os.Remove(tempRootfsPath)
		return nil, fmt.Errorf("failed to setup NBD device: %w", err)
	}

	// Mount the NBD device to the specified path
	if err := m.mountDevice(devicePath, mountPath); err != nil {
		// Clean up on error
		m.disconnectNBDDevice(devicePath)
		m.devicePool.ReleaseDevice(deviceSlot)
		os.Remove(tempRootfsPath)
		return nil, fmt.Errorf("failed to mount device to %s: %w", mountPath, err)
	}

	mountInfo := &MountInfo{
		TemplateID:   templateID,
		BuildID:      buildID,
		MountPath:    mountPath,
		DevicePath:   devicePath,
		DeviceSlot:   deviceSlot,
		TempFilePath: tempRootfsPath,
	}

	m.activeMounts[mountPath] = mountInfo

	m.logger.Info("Template mounted successfully",
		zap.String("template_id", templateID),
		zap.String("build_id", buildID),
		zap.String("mount_path", mountPath),
		zap.String("device_path", devicePath))

	return mountInfo, nil
}

// UnmountTemplate unmounts a template and cleans up resources
func (m *Manager) UnmountTemplate(mountPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mountInfo, exists := m.activeMounts[mountPath]
	if !exists {
		return fmt.Errorf("no mount found at path %s", mountPath)
	}

	// Unmount the filesystem
	if err := m.unmountPath(mountPath); err != nil {
		m.logger.Error("Failed to unmount filesystem", zap.String("mount_path", mountPath), zap.Error(err))
		// Continue with cleanup even if unmount fails
	}

	// Disconnect NBD device
	if err := m.disconnectNBDDevice(mountInfo.DevicePath); err != nil {
		m.logger.Error("Failed to disconnect NBD device", zap.String("device_path", mountInfo.DevicePath), zap.Error(err))
	}

	// Release device back to pool
	if err := m.devicePool.ReleaseDevice(mountInfo.DeviceSlot); err != nil {
		m.logger.Error("Failed to release NBD device", zap.Uint32("device_slot", mountInfo.DeviceSlot), zap.Error(err))
	}

	// Clean up temporary file
	if err := os.Remove(mountInfo.TempFilePath); err != nil {
		m.logger.Warn("Failed to clean up temporary file", zap.String("temp_file", mountInfo.TempFilePath), zap.Error(err))
	}

	delete(m.activeMounts, mountPath)

	m.logger.Info("Template unmounted successfully",
		zap.String("template_id", mountInfo.TemplateID),
		zap.String("build_id", mountInfo.BuildID),
		zap.String("mount_path", mountPath))

	return nil
}

// UnmountAll unmounts all active mounts
func (m *Manager) UnmountAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for mountPath := range m.activeMounts {
		if err := m.unmountTemplate(mountPath); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to unmount some templates: %v", errs)
	}

	return nil
}

// ListMounts returns information about all active mounts
func (m *Manager) ListMounts() []*MountInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mounts := make([]*MountInfo, 0, len(m.activeMounts))
	for _, mountInfo := range m.activeMounts {
		mounts = append(mounts, mountInfo)
	}

	return mounts
}

// unmountTemplate is the internal implementation without locking
func (m *Manager) unmountTemplate(mountPath string) error {
	mountInfo, exists := m.activeMounts[mountPath]
	if !exists {
		return fmt.Errorf("no mount found at path %s", mountPath)
	}

	// Unmount the filesystem
	if err := m.unmountPath(mountPath); err != nil {
		m.logger.Error("Failed to unmount filesystem", zap.String("mount_path", mountPath), zap.Error(err))
	}

	// Disconnect NBD device
	if err := m.disconnectNBDDevice(mountInfo.DevicePath); err != nil {
		m.logger.Error("Failed to disconnect NBD device", zap.String("device_path", mountInfo.DevicePath), zap.Error(err))
	}

	// Release device back to pool
	if err := m.devicePool.ReleaseDevice(mountInfo.DeviceSlot); err != nil {
		m.logger.Error("Failed to release NBD device", zap.Uint32("device_slot", mountInfo.DeviceSlot), zap.Error(err))
	}

	// Clean up temporary file
	if err := os.Remove(mountInfo.TempFilePath); err != nil {
		m.logger.Warn("Failed to clean up temporary file", zap.String("temp_file", mountInfo.TempFilePath), zap.Error(err))
	}

	delete(m.activeMounts, mountPath)

	return nil
}

// setupNBDDevice connects a file to an NBD device using qemu-nbd
func (m *Manager) setupNBDDevice(filePath, devicePath string) error {
	cmd := exec.Command("qemu-nbd", "--connect="+devicePath, "--format=raw", filePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to setup NBD device %s for file %s: %w, output: %s", devicePath, filePath, err, string(output))
	}
	return nil
}

// mountDevice mounts a block device to a directory
func (m *Manager) mountDevice(devicePath, mountPath string) error {
	// Try to mount as ext4 first, which is the expected filesystem type for templates
	if err := syscall.Mount(devicePath, mountPath, "ext4", 0, ""); err != nil {
		// If ext4 mount fails, try with mount command for better error handling
		cmd := exec.Command("mount", "-t", "ext4", devicePath, mountPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to mount device %s to %s: %w, output: %s", devicePath, mountPath, err, string(output))
		}
	}
	return nil
}

// unmountPath unmounts a filesystem from the specified path
func (m *Manager) unmountPath(mountPath string) error {
	// Try syscall first
	if err := syscall.Unmount(mountPath, 0); err != nil {
		// If syscall fails, try umount command
		cmd := exec.Command("umount", mountPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to unmount %s: %w, output: %s", mountPath, err, string(output))
		}
	}
	return nil
}

// disconnectNBDDevice disconnects an NBD device
func (m *Manager) disconnectNBDDevice(devicePath string) error {
	cmd := exec.Command("qemu-nbd", "--disconnect", devicePath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to disconnect NBD device %s: %w, output: %s", devicePath, err, string(output))
	}
	return nil
}

// Close cleans up all mounts and shuts down the manager
func (m *Manager) Close(ctx context.Context) error {
	return m.UnmountAll()
}