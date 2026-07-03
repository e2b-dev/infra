//go:build linux

package backend

import "fmt"

// NewBackend creates the backend for the given type and root mount path.
//
// backendType is provided via the VOLUME_BACKEND_TYPE env var (default "local").
// root is the host path for this volume type from PERSISTENT_VOLUME_MOUNTS.
func NewBackend(backendType, root string) (Backend, error) {
	switch backendType {
	case "local":
		return NewLocalBackend(root), nil
	case "juicefs":
		return NewJuiceFSBackend(root), nil
	case "cephfs", "ceph":
		return NewCephFSBackend(root), nil
	case "glusterfs":
		return NewGlusterFSBackend(root), nil
	case "seaweedfs":
		return NewSeaweedFSBackend(root), nil
	case "beegfs":
		return NewBeeGFSBackend(root), nil
	default:
		return nil, fmt.Errorf(
			"unknown volume backend type %q (supported: local, juicefs, cephfs, glusterfs, seaweedfs, beegfs)",
			backendType,
		)
	}
}
