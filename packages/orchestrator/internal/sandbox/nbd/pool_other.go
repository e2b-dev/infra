//go:build !linux
// +build !linux

package nbd

import (
	"context"
	"errors"
	"sync"
)

type ErrDeviceInUse struct{}

func (ErrDeviceInUse) Error() string {
	return "device in use"
}

type (
	DevicePath = string
	DeviceSlot = uint32
)

type DevicePool struct{}

var MustGetDevicePool = sync.OnceValue(func() *DevicePool {
	panic("nbd module is not supported on this platform")
})

func (d *DevicePool) Populate() error {
	return errors.New("platform does not support nbd")
}

func (d *DevicePool) GetDevice(ctx context.Context) (DeviceSlot, error) {
	return 0, errors.New("platform does not support nbd")
}

func (d *DevicePool) ReleaseDevice(idx DeviceSlot) error {
	return errors.New("platform does not support nbd")
}

func GetDevicePath(slot DeviceSlot) DevicePath {
	return ""
}

func GetDeviceSlot(path DevicePath) (DeviceSlot, error) {
	return 0, errors.New("platform does not support nbd")
}
