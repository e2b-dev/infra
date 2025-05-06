//go:build !linux
// +build !linux

package build

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/trace"
)

var fcAddr = "127.0.0.1:5150"

type Snapshot struct {
}

func NewSnapshot(ctx context.Context, tracer trace.Tracer, env *Env, network *FCNetwork, rootfs *Rootfs) (*Snapshot, error) {
	return nil, errors.New("snapshot is not supported on this platform")
}
