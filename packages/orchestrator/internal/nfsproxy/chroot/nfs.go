package chroot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sync"

	"github.com/go-git/go-billy/v5"
	"github.com/google/uuid"
	"github.com/willscott/go-nfs"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var (
	ErrVolumeNotFound   = errors.New("volume not found")
	ErrInvalidTeamID    = errors.New("invalid team ID")
	ErrVolumeID         = errors.New("invalid volume ID")
	ErrInvalidMountPath = errors.New("invalid mount path")
	ErrUnknownSandbox   = errors.New("unknown sandbox")
)

type NFSHandler struct {
	mu sync.Mutex

	builder   *chrooted.Builder
	sandboxes *sandbox.Map

	chrootsBySandboxID map[string][]*chrooted.Chrooted
}

var _ nfs.Handler = (*NFSHandler)(nil)

func (h *NFSHandler) OnInsert(_ *sandbox.Sandbox) {
}

func (h *NFSHandler) OnRemove(sandboxID string) {
	h.mu.Lock()
	chroots := h.chrootsBySandboxID[sandboxID]
	delete(h.chrootsBySandboxID, sandboxID)
	h.mu.Unlock()

	for _, chroot := range chroots {
		err := chroot.Close()
		if err != nil {
			logger.L().Warn(context.Background(), "failed to close chroot",
				logger.WithSandboxID(sandboxID),
				zap.String("path", chroot.Root()),
				zap.Error(err),
			)
		}
	}
}

func NewNFSHandler(
	builder *chrooted.Builder,
	sandboxes *sandbox.Map,
) *NFSHandler {
	h := &NFSHandler{
		builder:            builder,
		sandboxes:          sandboxes,
		chrootsBySandboxID: make(map[string][]*chrooted.Chrooted),
	}

	sandboxes.Subscribe(h)

	return h
}

func (h *NFSHandler) Mount(
	ctx context.Context,
	conn net.Conn,
	request nfs.MountRequest,
) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	fs, err := h.getChroot(ctx, conn.RemoteAddr(), request)
	if err != nil {
		logger.L().Warn(ctx, "failed to get path",
			zap.String("request", string(request.Dirpath)),
			zap.Error(err))

		return nfs.MountStatusErrAcces, mountFailedFS{}, nil
	}

	return nfs.MountStatusOk, wrapChrooted(fs), nil
}

var mountPath = regexp.MustCompile(`^/[^/]+$`)

func (h *NFSHandler) getChroot(ctx context.Context, remoteAddr net.Addr, request nfs.MountRequest) (*chrooted.Chrooted, error) {
	sbx, err := h.sandboxes.GetByHostPort(remoteAddr.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnknownSandbox, err)
	}

	// normalize the mount path
	requestedPath := string(request.Dirpath)
	regexpMatch := mountPath.MatchString(requestedPath)
	if !regexpMatch {
		return nil, fmt.Errorf(`%w: expected "/volume_name", got %q`, ErrInvalidMountPath, requestedPath)
	}

	volumeName := requestedPath[1:]

	// find the local volume mount
	var volumeMount *sandbox.VolumeMountConfig
	for _, sbxVolumeMount := range sbx.Config.VolumeMounts {
		if sbxVolumeMount.Name == volumeName {
			volumeMount = &sbxVolumeMount

			break
		}
	}
	if volumeMount == nil {
		return nil, fmt.Errorf("failed to mount %q: %w", volumeName, ErrVolumeNotFound)
	}

	teamID, ok := internal.TryParseUUID(sbx.Metadata.Runtime.TeamID)
	if !ok {
		return nil, ErrInvalidTeamID
	}

	if volumeMount.ID == uuid.Nil {
		return nil, ErrVolumeID
	}

	fs, err := h.builder.Chroot(ctx, volumeMount.Type, teamID, volumeMount.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to mount %q: %w", volumeName, err)
	}

	sandboxID := sbx.Metadata.Runtime.SandboxID
	h.mu.Lock()
	h.chrootsBySandboxID[sandboxID] = append(h.chrootsBySandboxID[sandboxID], fs)
	h.mu.Unlock()

	return fs, nil
}

func (h *NFSHandler) Change(_ context.Context, filesystem billy.Filesystem) billy.Change {
	for {
		isolated, ok := filesystem.(*wrappedFS)
		if ok {
			return wrapChange(isolated.chroot)
		}

		unwrappable, ok := filesystem.(interface{ Unwrap() billy.Filesystem })
		if !ok {
			panic(fmt.Sprintf("no idea how to find an *Chrooted from this filesystem: %T", filesystem))
		}

		filesystem = unwrappable.Unwrap()
	}
}

// FSStat describes the state of the exported file system. Things like total files, total bytes, available bytes, etc.
// We offer volumes that are unlimited in size, so we leave all values to their defaults, which is 1 << 62.
func (h *NFSHandler) FSStat(_ context.Context, _ billy.Filesystem, _ *nfs.FSStat) error {
	return nil
}

func (h *NFSHandler) ToHandle(_ context.Context, _ billy.Filesystem, _ []string) []byte {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) FromHandle(_ context.Context, _ []byte) (billy.Filesystem, []string, error) {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) InvalidateHandle(_ context.Context, _ billy.Filesystem, _ []byte) error {
	panic("this should be intercepted by the caching handler")
}

func (h *NFSHandler) HandleLimit() int {
	panic("this should be intercepted by the caching handler")
}
