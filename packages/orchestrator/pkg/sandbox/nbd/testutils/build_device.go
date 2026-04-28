package testutils

import (
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/nbdutil"
)

// BuildDevice is an alias for nbdutil.BuildDevice for backward compatibility.
type BuildDevice = nbdutil.BuildDevice

// NewBuildDevice re-exports nbdutil.NewBuildDevice for backward compatibility.
var NewBuildDevice = nbdutil.NewBuildDevice
