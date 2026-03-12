package nfsproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/willscott/go-nfs"
	"github.com/willscott/go-nfs/helpers"

	"github.com/e2b-dev/infra/packages/orchestrator/internal"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/chrooted"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/chroot"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/logged"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfsproxy/recovery"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

const cacheLimit = 1024

type Proxy struct {
	server *nfs.Server
}

var (
	ErrVolumeNotFound = errors.New("volume not found")
	ErrInvalidTeamID  = errors.New("invalid team ID")
	ErrVolumeID       = errors.New("invalid volume ID")
)

func NewProxy(ctx context.Context, cache *chrooted.Tracker, sandboxes *sandbox.Map) (*Proxy, error) {
	// actual nfs handler
	var handler nfs.Handler = chroot.NewNFSHandler(chrootCallback(cache, sandboxes))

	// wrap the handler in middleware
	handler = helpers.NewCachingHandler(handler, cacheLimit)
	handler = logged.WrapWithLogging(ctx, handler)
	handler = recovery.WrapWithRecovery(ctx, handler)

	s := &nfs.Server{
		Handler: handler,
		Context: ctx,
	}

	return &Proxy{server: s}, nil
}

func (p *Proxy) Serve(lis net.Listener) error {
	if err := p.server.Serve(lis); err != nil {
		if strings.Contains(err.Error(), "use of closed network connection") {
			return nil
		}

		return err
	}

	return nil
}

var mountPath = regexp.MustCompile(`^/[^/]+$`)

var (
	ErrInvalidMountPath = errors.New("invalid mount path")
	ErrUnknownSandbox   = errors.New("unknown sandbox")
)

func chrootCallback(tracker *chrooted.Tracker, sandboxes *sandbox.Map) chroot.GetFilesystem {
	return func(ctx context.Context, remoteAddr net.Addr, request nfs.MountRequest) (*chrooted.Chrooted, error) {
		sbx, err := sandboxes.GetByHostPort(remoteAddr.String())
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

		return tracker.Get(ctx, volumeMount.Type, teamID, volumeMount.ID)
	}
}
