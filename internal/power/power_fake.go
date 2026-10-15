//go:build darwin && arm64 && doubleshot_test

package power

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Compiled only into explicitly tagged test binaries. Production never reads
// these environment variables and never links this implementation.
type Controller struct {
	restores int
	idle     bool
}

func Open() (*Controller, error) {
	f := &Controller{}
	if err := record("open"); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *Controller) PreventIdle() error {
	if err := checkLock(syscall.LOCK_EX); err != nil {
		return err
	}
	if os.Getenv("DOUBLESHOT_TEST_FAIL_IDLE") == "1" {
		return errors.New("injected idle assertion failure")
	}
	if err := record("idle-acquire"); err != nil {
		return err
	}
	f.idle = true
	return nil
}
func (f *Controller) ReleaseIdle() error {
	if !f.idle {
		return nil
	}
	if err := record("idle-release"); err != nil {
		return err
	}
	f.idle = false
	return nil
}
func (f *Controller) setLidOverride(enabled bool) error {
	if enabled {
		if err := checkLock(syscall.LOCK_EX); err != nil {
			return err
		}
		if os.Getenv("DOUBLESHOT_TEST_FAIL_SET") == "1" {
			return errors.New("injected arming failure")
		}
		return record("set")
	}
	if err := checkLock(syscall.LOCK_SH); err != nil {
		return err
	}
	if delay, err := time.ParseDuration(os.Getenv("DOUBLESHOT_TEST_RESTORE_DELAY")); err == nil {
		if err := record("restore-start"); err != nil {
			return err
		}
		time.Sleep(delay)
	}
	f.restores++
	failures, _ := strconv.Atoi(os.Getenv("DOUBLESHOT_TEST_FAIL_RESTORE"))
	if f.restores <= failures {
		record("restore-failed")
		return errors.New("injected restoration failure")
	}
	return record("clear")
}
func (f *Controller) Close() error { return errors.Join(f.ReleaseIdle(), record("close")) }

func checkLock(conflicting int) error {
	file, err := os.OpenFile(filepath.Join(os.Getenv("DOUBLESHOT_TEST_DIR"), "doubleshot", "members.lock"), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), conflicting|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	}
	if err == nil {
		record("invalid-lock-ownership")
	}
	return fmt.Errorf("power operation without required membership lock: %v", err)
}

func record(operation string) error {
	file, err := os.OpenFile(os.Getenv("DOUBLESHOT_TEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	return json.NewEncoder(file).Encode(struct {
		PID int
		Op  string
	}{os.Getpid(), operation})
}

func UserTempDir() (string, error) {
	if directory := os.Getenv("DOUBLESHOT_TEST_DIR"); directory != "" {
		return directory, nil
	}
	return "", errors.New("test directory not configured")
}
func ReapplyInterval() time.Duration { return 50 * time.Millisecond }
func RetryInterval() time.Duration   { return 20 * time.Millisecond }

func ReadStatus() (Status, error) {
	result := Status{ExternalDisplay: No, ACPower: Yes, LidClosed: No}
	if raw := os.Getenv("DOUBLESHOT_TEST_STATUS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return Status{ExternalDisplay: Unknown, ACPower: Unknown, LidClosed: Unknown}, err
		}
	}
	return result, nil
}
