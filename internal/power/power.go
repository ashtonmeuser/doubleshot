//go:build darwin && arm64 && cgo && !doubleshot_test

package power

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation -framework CoreGraphics
#include "power.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"time"
)

// Controller owns the native connection and idle assertion. The test build
// substitutes a fake implementation with the same API instead of an interface.
type Controller struct{ handle, assertion C.uint32_t }

func Open() (*Controller, error) {
	n := &Controller{}
	if code := C.ds_power_open(&n.handle); code != 0 {
		return nil, nativeError("open IOKit connection", code)
	}
	return n, nil
}
func (n *Controller) setLidOverride(enabled bool) error {
	var value C.int
	if enabled {
		value = 1
	}
	return nativeError("set lid override", C.ds_power_set(n.handle, value))
}
func (n *Controller) PreventIdle() error {
	return nativeError("prevent idle sleep", C.ds_idle_create(&n.assertion))
}
func (n *Controller) ReleaseIdle() error {
	if n.assertion == 0 {
		return nil
	}
	err := nativeError("release idle assertion", C.ds_idle_release(n.assertion))
	if err == nil {
		n.assertion = 0
	}
	return err
}
func (n *Controller) Close() error {
	return errors.Join(n.ReleaseIdle(), nativeError("close IOKit connection", C.ds_power_close(n.handle)))
}
func nativeError(operation string, code C.int32_t) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s (0x%08x)", operation, C.GoString(C.ds_power_error(code)), uint32(code))
}
func UserTempDir() (string, error) {
	var path [4096]C.char
	if C.ds_user_temp(&path[0], C.size_t(len(path))) != 0 {
		return "", fmt.Errorf("cannot determine macOS per-user temporary directory")
	}
	return C.GoString(&path[0]), nil
}
func ReapplyInterval() time.Duration { return time.Second }
func RetryInterval() time.Duration   { return time.Second }

// ReadStatus reads registry/display/power information only. It does not open a
// root-domain user client, create assertions, or change any sleep policy.
func ReadStatus() (Status, error) {
	var raw C.ds_status
	code := C.ds_read_status(&raw)
	return Status{
		ExternalDisplay: State(raw.external_display), ACPower: State(raw.ac_power),
		LidClosed: State(raw.lid_closed),
	}, nativeError("read power state", code)
}
