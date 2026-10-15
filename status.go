//go:build darwin && arm64

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"doubleshot/internal/power"
)

func printStatus(output io.Writer) error {
	hardware, hardwareErr := power.ReadStatus()
	activity := "unknown"
	base, lockErr := power.UserTempDir()
	if lockErr == nil {
		activity, lockErr = membershipStatus(base)
	}
	_, writeErr := fmt.Fprintf(output,
		"External monitor: %s\nAC power: %s\nLid: %s\nDoubleshot sessions (this user): %s\n",
		stateText(hardware.ExternalDisplay, "yes", "no"),
		stateText(hardware.ACPower, "yes", "no"),
		stateText(hardware.LidClosed, "closed", "open"), activity)
	return errors.Join(hardwareErr, lockErr, writeErr)
}

func stateText(state power.State, yes, no string) string {
	switch state {
	case power.Yes:
		return yes
	case power.No:
		return no
	default:
		return "unknown"
	}
}

func membershipStatus(base string) (string, error) {
	file, err := membershipFile(base, false)
	if errors.Is(err, os.ErrNotExist) {
		return "none", nil
	}
	if err != nil {
		return "unknown", err
	}
	defer file.Close()
	// Darwin F_GETLK reports flock locks too (with PID -1), without taking a
	// lock. A probing LOCK_EX could steal the final guardian's cleanup turn.
	query := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	for {
		err = syscall.FcntlFlock(file.Fd(), syscall.F_GETLK, &query)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return "unknown", err
	}
	switch query.Type {
	case syscall.F_UNLCK:
		return "none", nil
	case syscall.F_RDLCK:
		return "active", nil
	case syscall.F_WRLCK:
		return "cleanup in progress", nil
	default:
		return "unknown", fmt.Errorf("unexpected membership lock type: %d", query.Type)
	}
}
