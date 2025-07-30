# Template Mounting Implementation

This implementation provides functionality to mount templates using NBD (Network Block Device) to paths accessible by the parent operating system. This allows templates to be inspected, modified, or used as data sources during the template building process or in other orchestrator operations.

## Overview

The implementation consists of several key components:

1. **MOUNT Command** (`packages/orchestrator/internal/template/build/command/mount.go`)
2. **UNMOUNT Command** (`packages/orchestrator/internal/template/build/command/unmount.go`)
3. **Mount Manager** (`packages/orchestrator/internal/template/mount/manager.go`)

## Architecture

### How it Works

1. **Template Retrieval**: The system downloads the template's rootfs (ext4 filesystem) from storage to a temporary local file
2. **NBD Device Allocation**: An NBD device is allocated from the device pool (e.g., `/dev/nbd0`)
3. **NBD Connection**: The rootfs file is connected to the NBD device using `qemu-nbd`
4. **Filesystem Mount**: The NBD device is mounted to the specified path using standard Linux mount operations
5. **Access**: The template filesystem is now accessible at the mount path by the parent OS

### Components

#### MOUNT Command

- **Usage**: `MOUNT template_id build_id mount_path`
- **Purpose**: Mounts a template's filesystem to a specified path
- **Example**: `MOUNT my-template build-12345 /mnt/external-template`

#### UNMOUNT Command

- **Usage**: `UNMOUNT mount_path`
- **Purpose**: Unmounts a template and cleans up associated resources
- **Example**: `UNMOUNT /mnt/external-template`

#### Mount Manager

Provides programmatic access to mounting functionality with proper resource management:

- Thread-safe mount/unmount operations
- Automatic cleanup of temporary files and NBD devices
- Mount tracking and management
- Graceful shutdown with cleanup of all active mounts

## Usage Examples

### In Template Steps

```yaml
steps:
  - type: MOUNT
    args: ["source-template", "build-abc123", "/mnt/source"]
    
  - type: RUN
    args: ["cp -r /mnt/source/app /current-build/"]
    
  - type: RUN
    args: ["cat /mnt/source/config/app.conf > /current-build/config/imported.conf"]
    
  - type: UNMOUNT
    args: ["/mnt/source"]
```

### Programmatic Usage

```go
package main

import (
    "context"
    "fmt"
    
    "github.com/e2b-dev/infra/packages/orchestrator/internal/template/mount"
    // ... other imports
)

func main() {
    ctx := context.Background()
    
    // Initialize dependencies
    logger := zap.L()
    templateStorage, _ := storage.GetTemplateStorageProvider(ctx, nil)
    devicePool, _ := nbd.NewPool(ctx, 64)
    
    // Create mount manager
    mountManager := mount.NewManager(logger, templateStorage, devicePool)
    defer mountManager.Close(ctx)
    
    // Mount a template
    mountInfo, err := mountManager.MountTemplate(ctx, "template-id", "build-id", "/mnt/my-template")
    if err != nil {
        panic(err)
    }
    
    // Access the mounted filesystem
    // Files are now accessible at /mnt/my-template/
    
    // Unmount when done
    mountManager.UnmountTemplate("/mnt/my-template")
}
```

## Prerequisites

### System Requirements

1. **NBD Module**: The NBD kernel module must be loaded
   ```bash
   sudo modprobe nbd nbds_max=4096
   ```

2. **qemu-nbd**: The `qemu-nbd` utility must be installed
   ```bash
   # Ubuntu/Debian
   sudo apt-get install qemu-utils
   
   # CentOS/RHEL
   sudo yum install qemu-img
   ```

3. **Permissions**: The process must have sufficient permissions to:
   - Access NBD devices (`/dev/nbdX`)
   - Mount/unmount filesystems
   - Create temporary files

### Configuration

The implementation uses the existing orchestrator infrastructure:

- **Template Storage**: Configured via `STORAGE_PROVIDER` environment variable
- **NBD Device Pool**: Managed by the orchestrator's existing NBD pool
- **Logging**: Uses the standard zap logger

## Key Features

### Resource Management

- **Automatic Cleanup**: Temporary files and NBD devices are automatically cleaned up
- **Error Handling**: Comprehensive error handling with proper resource cleanup on failures
- **Device Pool Integration**: Uses the existing NBD device pool for efficient device management

### Safety Features

- **Mount Path Validation**: Ensures mount paths are absolute and safe
- **Duplicate Mount Detection**: Prevents mounting to the same path twice
- **Graceful Shutdown**: Properly unmounts all active mounts on shutdown

### Monitoring and Logging

- **Detailed Logging**: Comprehensive logging of mount/unmount operations
- **Mount Tracking**: Ability to list all active mounts
- **Error Reporting**: Clear error messages with context

## Limitations and Considerations

### Current Limitations

1. **Read-Only Recommended**: While the mounted filesystem is writable, changes are made to the temporary copy and not persisted back to storage
2. **Temporary Files**: Each mount creates a temporary copy of the rootfs, which requires local disk space
3. **NBD Device Limit**: Limited by the number of available NBD devices (configured via `nbds_max`)

### Performance Considerations

1. **Download Time**: Initial template mounting requires downloading the rootfs from storage
2. **Disk Space**: Each mounted template requires space for a full rootfs copy
3. **Memory Usage**: Large templates may impact system memory usage

### Security Considerations

1. **File Permissions**: Mounted files inherit permissions from the template
2. **Mount Path Security**: Mount paths should be carefully chosen to avoid conflicts
3. **Cleanup**: Proper cleanup is essential to avoid resource leaks

## Troubleshooting

### Common Issues

1. **NBD Module Not Loaded**
   ```bash
   sudo modprobe nbd nbds_max=4096
   ```

2. **Permission Denied**
   - Ensure the process has sufficient privileges
   - Check mount point permissions

3. **Device Busy**
   - Ensure proper unmounting of previously mounted templates
   - Check for processes accessing the mount point

4. **qemu-nbd Not Found**
   ```bash
   sudo apt-get install qemu-utils
   ```

### Debugging

Enable debug logging to get detailed information about mount operations:

```go
logger := zap.NewDevelopment()
mountManager := mount.NewManager(logger, templateStorage, devicePool)
```

## Future Enhancements

### Potential Improvements

1. **Caching**: Implement local caching of frequently accessed templates
2. **Copy-on-Write**: Use CoW functionality to avoid full rootfs copies
3. **Persistence**: Option to persist changes back to storage
4. **Performance Optimization**: Stream mounting without full download
5. **Mount Options**: Support for read-only and other mount options

### Integration Opportunities

1. **Template Inspection Tools**: Build tools that use mounting for template analysis
2. **Template Merging**: Use mounting to merge multiple templates
3. **Development Tools**: Local template development and testing
4. **Backup and Recovery**: Template backup and restore operations

## API Reference

### Mount Manager

#### `NewManager(logger, templateStorage, devicePool) *Manager`
Creates a new mount manager instance.

#### `MountTemplate(ctx, templateID, buildID, mountPath) (*MountInfo, error)`
Mounts a template to the specified path.

#### `UnmountTemplate(mountPath) error`
Unmounts a template from the specified path.

#### `ListMounts() []*MountInfo`
Returns information about all active mounts.

#### `UnmountAll() error`
Unmounts all active mounts.

#### `Close(ctx) error`
Shuts down the manager and cleans up all resources.

### MountInfo Structure

```go
type MountInfo struct {
    TemplateID   string  // Template identifier
    BuildID      string  // Build identifier
    MountPath    string  // Path where template is mounted
    DevicePath   string  // NBD device path (e.g., /dev/nbd0)
    DeviceSlot   uint32  // NBD device slot number
    TempFilePath string  // Path to temporary rootfs file
}
```