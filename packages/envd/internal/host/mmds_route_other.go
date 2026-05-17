//go:build !linux

package host

import "context"

func PinMMDSRoute(_ context.Context) error { return nil }
