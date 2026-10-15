//go:build darwin && arm64 && doubleshot_test

package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func configurePowerTest(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	t.Setenv("DOUBLESHOT_TEST_DIR", f.dir)
	t.Setenv("DOUBLESHOT_TEST_LOG", f.log)
	return f
}

func TestGuardianTimeout(t *testing.T) {
	f := configurePowerTest(t)
	var ready bytes.Buffer
	if err := guard(os.Getppid(), &ready, guardianConfig{Timeout: 120 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if ready.String() != "R" {
		t.Fatalf("readiness: %q", ready.String())
	}
	if f.count("set") < 2 || f.count("idle-acquire") != 1 || f.count("idle-release") != 1 || f.count("clear") != 1 || f.count("close") != 1 {
		t.Fatalf("incorrect lifecycle: %v", f.events())
	}
}

func TestGuardianFailures(t *testing.T) {
	for _, tc := range []struct {
		name, env, value, message string
		ready                     io.Writer
		acquired                  int
	}{
		{"idle", "DOUBLESHOT_TEST_FAIL_IDLE", "1", "idle assertion failure", io.Discard, 0},
		{"arming", "DOUBLESHOT_TEST_FAIL_SET", "1", "arming failure", io.Discard, 1},
		{"readiness", "", "", "acknowledge readiness", failingWriter{}, 1},
		{"restoration", "DOUBLESHOT_TEST_FAIL_RESTORE", "3", "cleanup failed", io.Discard, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := configurePowerTest(t)
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			err := guard(os.Getppid(), tc.ready, guardianConfig{Timeout: time.Millisecond})
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error: %v", err)
			}
			if f.count("idle-acquire") != tc.acquired || f.count("idle-release") != tc.acquired || f.count("close") != 1 {
				t.Fatalf("leaked resources: %v", f.events())
			}
			if tc.name == "restoration" {
				if f.count("restore-failed") != 3 || f.count("clear") != 0 {
					t.Fatalf("retries: %v", f.events())
				}
			} else if f.count("clear") != 1 {
				t.Fatalf("missing cleanup: %v", f.events())
			}
		})
	}
}

func TestGuardianRestoreRetry(t *testing.T) {
	f := configurePowerTest(t)
	t.Setenv("DOUBLESHOT_TEST_FAIL_RESTORE", "2")
	if err := guard(os.Getppid(), io.Discard, guardianConfig{Timeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if f.count("restore-failed") != 2 || f.count("clear") != 1 {
		t.Fatalf("retries: %v", f.events())
	}
}

func TestGuardianRestorationPolicy(t *testing.T) {
	for _, test := range []struct {
		name, status     string
		clear, wantError bool
	}{
		{"no monitor", `{"ExternalDisplay":0,"ACPower":1}`, true, false},
		{"monitor on battery", `{"ExternalDisplay":1,"ACPower":0}`, true, false},
		{"native clamshell mode", `{"ExternalDisplay":1,"ACPower":1}`, false, false},
		{"unknown display", `{"ExternalDisplay":-1}`, false, true},
		{"failed query", `invalid-json`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := configurePowerTest(t)
			t.Setenv("DOUBLESHOT_TEST_STATUS", test.status)
			err := guard(os.Getppid(), io.Discard, guardianConfig{Timeout: time.Millisecond})
			if (err != nil) != test.wantError {
				t.Fatalf("cleanup error: %v", err)
			}
			if (f.count("clear") == 1) != test.clear || f.count("idle-release") != 1 || f.count("close") != 1 {
				t.Fatalf("incorrect cleanup: %v", f.events())
			}
		})
	}
}

func TestGuardianWatchedExit(t *testing.T) {
	f := configurePowerTest(t)
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	ready := writerFunc(func(data []byte) (int, error) {
		if string(data) != "R" {
			t.Fatalf("readiness: %q", data)
		}
		return len(data), child.Process.Kill()
	})
	started := time.Now()
	if err := guard(os.Getppid(), ready, guardianConfig{WaitPID: child.Process.Pid, Timeout: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= time.Second {
		t.Fatal("guardian waited for the fallback timeout instead of process exit")
	}
	if f.count("idle-release") != 1 || f.count("clear") != 1 {
		t.Fatalf("missing cleanup: %v", f.events())
	}
}

func TestGuardianPreservesOtherMember(t *testing.T) {
	f := configurePowerTest(t)
	other, err := openMembership(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := flock(other, syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	if err := guard(os.Getppid(), io.Discard, guardianConfig{Timeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if f.count("idle-release") != 1 || f.count("clear") != 0 || f.count("close") != 1 {
		t.Fatalf("disturbed another member: %v", f.events())
	}
}

func TestGuardianSetupErrors(t *testing.T) {
	for _, kind := range []string{"directory", "lock", "connection"} {
		t.Run(kind, func(t *testing.T) {
			f := configurePowerTest(t)
			switch kind {
			case "directory":
				t.Setenv("DOUBLESHOT_TEST_DIR", "")
			case "lock":
				file, err := openMembership(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Chmod(0666); err != nil {
					t.Fatal(err)
				}
				file.Close()
			case "connection":
				t.Setenv("DOUBLESHOT_TEST_LOG", f.dir) // Cannot open a directory as a log.
			}
			var ready bytes.Buffer
			if err := guard(os.Getppid(), &ready, guardianConfig{}); err == nil {
				t.Fatal("missing setup error")
			}
			if ready.Len() != 0 || len(f.events()) != 0 {
				t.Fatalf("setup acquired power: %q %v", ready.String(), f.events())
			}
		})
	}
}

func TestGuardianNoProtection(t *testing.T) {
	for _, pid := range []int{0, 1, os.Getpid()} {
		t.Run(strconv.Itoa(pid), func(t *testing.T) {
			f := configurePowerTest(t)
			var ready bytes.Buffer
			if err := guard(pid, &ready, guardianConfig{}); err != nil {
				t.Fatal(err)
			}
			if ready.Len() != 0 || len(f.events()) != 0 {
				t.Fatalf("unexpected protection: %q %v", ready.String(), f.events())
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("injected write failure") }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(data []byte) (int, error) { return f(data) }
