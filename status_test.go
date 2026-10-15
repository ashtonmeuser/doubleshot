//go:build darwin && arm64 && doubleshot_test

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"doubleshot/internal/power"
)

func TestMembershipStatus(t *testing.T) {
	base := t.TempDir()
	check := func(want string) {
		t.Helper()
		got, err := membershipStatus(base)
		if err != nil || got != want {
			t.Fatalf("status = %q, %v; want %q", got, err, want)
		}
	}
	check("none")
	if _, err := os.Stat(filepath.Join(base, "doubleshot")); !os.IsNotExist(err) {
		t.Fatalf("status created a directory: %v", err)
	}
	file, err := openMembership(base)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	check("none")
	if err := flock(file, syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	check("active")
	probe, err := openMembership(base)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if err := flock(probe, syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
		t.Fatalf("status disturbed shared membership: %v", err)
	}
	if err := flock(file, syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	check("cleanup in progress")
	if err := flock(file, syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	check("none")
	if err := file.Chmod(0666); err != nil {
		t.Fatal(err)
	}
	if got, err := membershipStatus(base); err == nil || got != "unknown" {
		t.Fatalf("unsafe lock: %q, %v", got, err)
	}
}

func TestStateText(t *testing.T) {
	for state, want := range map[power.State]string{power.Unknown: "unknown", power.No: "no", power.Yes: "yes"} {
		if got := stateText(state, "yes", "no"); got != want {
			t.Fatalf("%d: %q", state, got)
		}
	}
}

func TestStatusDoesNotTouchPower(t *testing.T) {
	f := newFixture(t)
	j := f.start("-s")
	if code := j.wait(t); code != 0 {
		t.Fatalf("exit %d: %s", code, j.err.String())
	}
	if !strings.Contains(j.out.String(), "Doubleshot sessions (this user): none\n") {
		t.Fatalf("output: %q", j.out.String())
	}
	if len(f.events()) != 0 {
		t.Fatalf("status touched power: %v", f.events())
	}
	if _, err := os.Stat(filepath.Join(f.dir, "doubleshot")); !os.IsNotExist(err) {
		t.Fatalf("status created a lock directory: %v", err)
	}
}

func TestStatusDuringProtection(t *testing.T) {
	f := newFixture(t)
	active := f.start("/bin/sleep", "30")
	f.until(func() bool { return f.count("set") > 0 })
	j := f.start("-s")
	if code := j.wait(t); code != 0 || !strings.Contains(j.out.String(), "sessions (this user): active\n") {
		t.Fatalf("status exit %d: %s %s", code, j.out.String(), j.err.String())
	}
	if f.count("open") != 1 || f.count("idle-acquire") != 1 {
		t.Fatalf("status acquired power: %v", f.events())
	}
	if err := active.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	active.wait(t)
	f.closed(1)
	if f.count("clear") != 1 {
		t.Fatalf("missing cleanup: %v", f.events())
	}
}

func TestStatusDuringCleanup(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_RESTORE_DELAY=500ms")
	active := f.start("/usr/bin/true")
	f.until(func() bool { return f.count("restore-start") == 1 })
	j := f.start("-s")
	if code := j.wait(t); code != 0 || !strings.Contains(j.out.String(), "sessions (this user): cleanup in progress\n") {
		t.Fatalf("status exit %d: %s %s", code, j.out.String(), j.err.String())
	}
	active.wait(t)
	f.closed(1)
	if f.count("open") != 1 || f.count("clear") != 1 {
		t.Fatalf("status disturbed cleanup: %v", f.events())
	}
}

func TestPrintStatus(t *testing.T) {
	f := configurePowerTest(t)
	var output bytes.Buffer
	if err := printStatus(&output); err != nil {
		t.Fatal(err)
	}
	want := "External monitor: no\nAC power: yes\nLid: open\nDoubleshot sessions (this user): none\n"
	if output.String() != want {
		t.Fatalf("output: %q", output.String())
	}
	if len(f.events()) != 0 {
		t.Fatalf("status changed power: %v", f.events())
	}
}

func TestPrintStatusErrors(t *testing.T) {
	for _, kind := range []string{"hardware", "directory", "lock", "writer"} {
		t.Run(kind, func(t *testing.T) {
			f := configurePowerTest(t)
			switch kind {
			case "hardware":
				t.Setenv("DOUBLESHOT_TEST_STATUS", "invalid-json")
			case "directory":
				t.Setenv("DOUBLESHOT_TEST_DIR", "")
			case "lock":
				lock, err := openMembership(f.dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := lock.Chmod(0666); err != nil {
					t.Fatal(err)
				}
				lock.Close()
			}
			var output bytes.Buffer
			var err error
			if kind == "writer" {
				err = printStatus(failingWriter{})
			} else {
				err = printStatus(&output)
			}
			if err == nil {
				t.Fatal("missing error")
			}
			if (kind == "directory" || kind == "lock") && !strings.Contains(output.String(), "sessions (this user): unknown\n") {
				t.Fatalf("failed query reported as inactive: %q", output.String())
			}
			if len(f.events()) != 0 {
				t.Fatalf("status changed power: %v", f.events())
			}
		})
	}
}

func TestPrintStatusUnknown(t *testing.T) {
	configurePowerTest(t)
	t.Setenv("DOUBLESHOT_TEST_STATUS", `{"ExternalDisplay":-1,"ACPower":-1,"LidClosed":-1}`)
	var output bytes.Buffer
	if err := printStatus(&output); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), ": unknown\n") != 3 {
		t.Fatalf("output: %q", output.String())
	}
}
