//go:build darwin && arm64

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"doubleshot/internal/power"
)

func guard(pid int, ready io.Writer, opts guardianConfig) (result error) {
	if pid <= 1 {
		return nil
	}
	pids := []int{pid}
	if opts.WaitPID > 0 && opts.WaitPID != pid {
		pids = append(pids, opts.WaitPID)
	}
	watch, err := watchProcesses(pids...)
	if errors.Is(err, syscall.ESRCH) {
		_, err := ready.Write([]byte{'D'})
		return err
	}
	if err != nil {
		return err
	}
	defer watch.close()
	if os.Getppid() != pid {
		return nil
	}
	base, err := power.UserTempDir()
	if err != nil {
		return err
	}
	lock, err := openMembership(base)
	if err != nil {
		return err
	}
	defer lock.Close()
	// Open the native connection before joining. An initialization failure must
	// not temporarily prevent an existing member from performing final cleanup.
	controller, err := power.Open()
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, controller.Close()) }()
	for {
		err = flock(lock, syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if exited, err := watch.exited(25 * time.Millisecond); err != nil || exited {
			return err
		}
	}
	defer func() { result = errors.Join(result, cleanupGuardian(lock, controller)) }()
	if exited, err := watch.exited(0); err != nil || exited {
		if err == nil {
			_, err = ready.Write([]byte{'D'})
		}
		return err
	}
	if err := controller.PreventIdle(); err != nil {
		return err
	}
	if err := controller.Disable(); err != nil {
		return err
	}
	var deadline time.Time
	if opts.Timeout > 0 {
		deadline = time.Now().Add(opts.Timeout)
	}
	if _, err := ready.Write([]byte{'R'}); err != nil {
		return fmt.Errorf("acknowledge readiness: %w", err)
	}
	failed := false
	for {
		wait := power.ReapplyInterval()
		if !deadline.IsZero() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil
			}
			if remaining < wait {
				wait = remaining
			}
		}
		if exited, err := watch.exited(wait); err != nil || exited {
			return err
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return nil
		}
		if err := controller.Disable(); err != nil {
			if !failed {
				fmt.Fprintln(os.Stderr, "doubleshot: lid protection unavailable; retrying:", err)
			}
			failed = true
		} else {
			failed = false
		}
	}
}

// cleanupGuardian releases this guardian's assertion before attempting shared
// lid-policy cleanup. The caller retains the file until this function returns.
func cleanupGuardian(lock *os.File, controller *power.Controller) error {
	result := controller.ReleaseIdle()
	// Withdraw SH before trying EX, so simultaneous departures cannot both
	// decline cleanup while still holding shared membership.
	if err := flock(lock, syscall.LOCK_UN); err != nil {
		return errors.Join(result, err)
	}
	if err := flock(lock, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return result
		}
		return errors.Join(result, err)
	}
	// EX remains held through restoration. New invocations must wait.
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = controller.Restore(); err == nil {
			return result
		}
		if attempt < 2 {
			time.Sleep(power.RetryInterval())
		}
	}
	return errors.Join(result, fmt.Errorf("cleanup failed; lid sleep may remain disabled: %w", err))
}

type processWatch int

func watchProcesses(pids ...int) (processWatch, error) {
	fd, err := syscall.Kqueue()
	if err != nil {
		return 0, err
	}
	syscall.CloseOnExec(fd)
	for _, pid := range pids {
		event := syscall.Kevent_t{Fflags: syscall.NOTE_EXIT}
		syscall.SetKevent(&event, pid, syscall.EVFILT_PROC, syscall.EV_ADD|syscall.EV_ENABLE)
		if _, err := syscall.Kevent(fd, []syscall.Kevent_t{event}, nil, nil); err != nil {
			syscall.Close(fd)
			return 0, fmt.Errorf("watch process %d: %w", pid, err)
		}
	}
	return processWatch(fd), nil
}

func (w processWatch) close() { syscall.Close(int(w)) }

func (w processWatch) exited(wait time.Duration) (bool, error) {
	deadline := time.Now().Add(wait)
	for {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		timeout := syscall.NsecToTimespec(remaining.Nanoseconds())
		var events [1]syscall.Kevent_t
		n, err := syscall.Kevent(int(w), nil, events[:], &timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return false, err
		}
		if n > 0 && events[0].Flags&syscall.EV_ERROR != 0 {
			return false, syscall.Errno(events[0].Data)
		}
		return n > 0, nil
	}
}
