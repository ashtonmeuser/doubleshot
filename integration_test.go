//go:build darwin && arm64

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var testBinary, helperBinary string

func TestMain(m *testing.M) {
	// Announce readiness from the actual signal target, with no shell or
	// foreground child between the announcement and the signal wait.
	if path := os.Getenv("DOUBLESHOT_TEST_SIGNAL_READY"); path != "" {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(time.Minute)
		os.Exit(1)
	}
	// The same Go test executable also acts as an argument recorder. No extra
	// source generator, interpreter or testing framework is needed.
	if path := os.Getenv("DOUBLESHOT_TEST_RECORD_ARGS"); path != "" {
		data, _ := json.Marshal(struct {
			PID    int
			Args   []string
			Marker string
		}{os.Getpid(), os.Args[1:], os.Getenv("DOUBLESHOT_TEST_ENV_MARKER")})
		if err := os.WriteFile(path, data, 0600); err != nil {
			os.Exit(1)
		}
		os.Exit(23)
	}
	directory, err := os.MkdirTemp("", "doubleshot-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBinary = filepath.Join(directory, "doubleshot")
	helperBinary, _ = os.Executable()
	buildArgs := []string{"build", "-tags", "doubleshot_test", "-o", testBinary}
	if raceEnabled {
		buildArgs = append(buildArgs, "-race")
	}
	cmd := exec.Command("go", append(buildArgs, ".")...)
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build test binary: %v\n%s", err, output)
		os.RemoveAll(directory)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}

type event struct {
	PID int
	Op  string
}
type job struct {
	cmd      *exec.Cmd
	out, err bytes.Buffer
	done     chan struct{}
	waitErr  error
}
type fixture struct {
	t        *testing.T
	dir, log string
	env      []string
	jobs     []*job
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir()}
	f.log = filepath.Join(f.dir, "power.log")
	f.env = append(os.Environ(), "DOUBLESHOT_TEST_DIR="+f.dir, "DOUBLESHOT_TEST_LOG="+f.log,
		"GORACE=atexit_sleep_ms=0")
	t.Cleanup(func() {
		for _, j := range f.jobs {
			select {
			case <-j.done:
			default:
				_ = j.cmd.Process.Kill()
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			allDone := true
			for _, j := range f.jobs {
				select {
				case <-j.done:
				default:
					allDone = false
				}
			}
			if allDone && f.count("open") == f.count("close") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		for _, j := range f.jobs {
			// Every fixture job has its own group; remove surviving descendants.
			_ = syscall.Kill(-j.cmd.Process.Pid, syscall.SIGKILL)
			select {
			case <-j.done:
			case <-time.After(time.Second):
				t.Errorf("job %d did not stop", j.cmd.Process.Pid)
			}
		}
		if f.count("invalid-lock-ownership") != 0 {
			t.Error("power operation violated lock ownership")
		}
		if f.count("idle-acquire") != f.count("idle-release") {
			t.Errorf("unbalanced idle assertions: %v", f.events())
		}
		active := 0
		for _, e := range f.events() {
			switch e.Op {
			case "idle-acquire":
				active++
			case "idle-release":
				active--
			case "clear":
				if active != 0 {
					t.Error("lid cleanup preceded idle assertion release")
				}
			}
		}
	})
	return f
}

func (f *fixture) start(args ...string) *job { return f.startProgram(testBinary, args...) }
func (f *fixture) startProgram(program string, args ...string) *job {
	return f.startInput(program, nil, args...)
}

func (f *fixture) startInput(program string, input *strings.Reader, args ...string) *job {
	f.t.Helper()
	j := &job{cmd: exec.Command(program, args...), done: make(chan struct{})}
	j.cmd.Env = f.env
	if input != nil {
		j.cmd.Stdin = input
	}
	j.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	j.cmd.Stdout = &j.out
	j.cmd.Stderr = &j.err
	j.cmd.WaitDelay = 2 * time.Second
	if err := j.cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	f.jobs = append(f.jobs, j)
	go func() { j.waitErr = j.cmd.Wait(); close(j.done) }()
	return j
}

func (j *job) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-j.done:
		if j.waitErr != nil {
			var exit *exec.ExitError
			if !errors.As(j.waitErr, &exit) {
				t.Fatalf("wait: %v; stderr=%s", j.waitErr, j.err.String())
			}
		}
		return j.cmd.ProcessState.ExitCode()
	case <-time.After(5 * time.Second):
		t.Fatal("job timed out")
		return -1
	}
}
func (j *job) running() bool {
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}
func (f *fixture) events() []event {
	data, _ := os.ReadFile(f.log)
	decoder := json.NewDecoder(bytes.NewReader(data))
	var events []event
	for {
		var e event
		if decoder.Decode(&e) != nil {
			break
		}
		events = append(events, e)
	}
	return events
}
func (f *fixture) count(operation string) int {
	count := 0
	for _, e := range f.events() {
		if e.Op == operation {
			count++
		}
	}
	return count
}

func (f *fixture) setters() int {
	pids := make(map[int]bool)
	for _, e := range f.events() {
		if e.Op == "set" {
			pids[e.PID] = true
		}
	}
	return len(pids)
}
func (f *fixture) until(predicate func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			for _, j := range f.jobs {
				select {
				case <-j.done:
					f.t.Logf("job stderr: %s", j.err.String())
				default:
				}
			}
			f.t.Fatalf("condition timed out; events=%v", f.events())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (f *fixture) closed(count int) { f.until(func() bool { return f.count("close") >= count }) }

func TestCommandArguments(t *testing.T) {
	for _, prefix := range [][]string{nil, {"--"}, {"-t", "123"}, {"-t", "123", "--"}} {
		t.Run(strings.Join(prefix, " "), func(t *testing.T) {
			f := newFixture(t)
			record := filepath.Join(f.dir, "args.json")
			f.env = append(f.env, "DOUBLESHOT_TEST_RECORD_ARGS="+record, "DOUBLESHOT_TEST_ENV_MARKER=original")
			commandArgs := []string{"-h", "-t", "2", "-w", "123", "--", "", "a b", "$(literal)", "line\nbreak"}
			args := append(append(append([]string{}, prefix...), helperBinary), commandArgs...)
			j := f.start(args...)
			if code := j.wait(t); code != 23 {
				t.Fatalf("exit=%d stderr=%s", code, j.err.String())
			}
			f.closed(1)
			var got struct {
				PID    int
				Args   []string
				Marker string
			}
			data, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(commandArgs)
			actual, _ := json.Marshal(got.Args)
			if !bytes.Equal(actual, want) || got.PID != j.cmd.Process.Pid || got.Marker != "original" {
				t.Fatalf("command arguments: %+v", got)
			}
			if f.count("idle-acquire") != 1 || f.count("idle-release") != 1 {
				t.Fatalf("idle lifecycle: %v", f.events())
			}
		})
	}
}

func TestGuardianProtocol(t *testing.T) {
	for _, test := range []struct {
		name    string
		config  []string
		message string
	}{
		{"missing configuration", nil, "invalid guardian configuration"},
		{"invalid timeout", []string{"invalid", "0"}, "invalid guardian configuration"},
		{"negative timeout", []string{"-1", "0"}, "invalid guardian configuration"},
		{"timeout overflow", []string{"9223372036854775808", "0"}, "invalid guardian configuration"},
		{"negative PID", []string{"0", "-1"}, "invalid guardian configuration"},
		{"PID overflow", []string{"0", "2147483648"}, "invalid guardian configuration"},
		{"extra configuration", []string{"0", "0", "extra"}, "invalid guardian configuration"},
		{"missing pipe", []string{"0", "0"}, "missing guardian readiness pipe"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			j := f.start(append([]string{guardianFlag}, test.config...)...)
			if code := j.wait(t); code != 1 || !strings.Contains(j.err.String(), test.message) {
				t.Fatalf("exit=%d stderr=%s", code, j.err.String())
			}
			if len(f.events()) != 0 {
				t.Fatalf("invalid invocation touched power: %v", f.events())
			}
		})
	}
}

func TestStatusErrorExit(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_STATUS=invalid-json")
	j := f.start("-s")
	if code := j.wait(t); code != 1 || !strings.Contains(j.err.String(), "doubleshot: status:") {
		t.Fatalf("exit=%d stderr=%s", code, j.err.String())
	}
	if len(f.events()) != 0 {
		t.Fatalf("status touched power: %v", f.events())
	}
}

func TestCommandIOAndExit(t *testing.T) {
	f := newFixture(t)
	j := f.start("/bin/sh", "-c", "printf hello; printf diagnostic >&2; exit 42")
	if code := j.wait(t); code != 42 {
		t.Fatalf("exit=%d", code)
	}
	f.closed(1)
	if j.out.String() != "hello" || j.err.String() != "diagnostic" {
		t.Fatalf("stdout=%q stderr=%q", j.out.String(), j.err.String())
	}
}

func TestStdin(t *testing.T) {
	f := newFixture(t)
	j := f.startInput(testBinary, strings.NewReader("input\n"), "/bin/cat")
	if code := j.wait(t); code != 0 || j.out.String() != "input\n" {
		t.Fatalf("stdin: exit=%d stdout=%q", code, j.out.String())
	}
	f.closed(1)
}

func TestTimeout(t *testing.T) {
	f := newFixture(t)
	begin := time.Now()
	if code := f.start("-t", "1").wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if time.Since(begin) < 800*time.Millisecond {
		t.Fatal("timeout returned early")
	}
	f.closed(1)
}

func TestWatchedPID(t *testing.T) {
	f := newFixture(t)
	watch := f.startProgram("/bin/sleep", "30")
	j := f.start("-w", strconv.Itoa(watch.cmd.Process.Pid))
	f.until(func() bool { return f.count("set") >= 3 })
	if !j.running() {
		t.Fatal("watch ended early")
	}
	_ = watch.cmd.Process.Kill()
	if code := j.wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	f.closed(1)
}

func TestTimeoutAndPID(t *testing.T) {
	f := newFixture(t)
	watch := f.startProgram("/bin/sleep", "30")
	if code := f.start("-t", "1", "-w", strconv.Itoa(watch.cmd.Process.Pid)).wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !watch.running() {
		t.Fatal("watched job should still run")
	}
	f.closed(1)
}

func TestCommandTimeoutDoesNotKillCommand(t *testing.T) {
	f := newFixture(t)
	finish := filepath.Join(f.dir, "finish")
	j := f.start("-t", "1", "-w", strconv.Itoa(os.Getpid()), "/bin/sh", "-c",
		`while [ ! -f "$1" ]; do sleep 0.02; done; exit 17`, "sh", finish)
	f.closed(1)
	if !j.running() {
		t.Fatal("timeout terminated the command")
	}
	if f.count("idle-release") != 1 || f.count("clear") < 1 {
		t.Fatalf("protection did not expire: %v", f.events())
	}
	if err := os.WriteFile(finish, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if code := j.wait(t); code != 17 {
		t.Fatalf("exit=%d", code)
	}
}

func TestCommandWatchedPIDDoesNotKillCommand(t *testing.T) {
	f := newFixture(t)
	watch := f.startProgram("/bin/sleep", "30")
	finish := filepath.Join(f.dir, "finish")
	j := f.start("-w", strconv.Itoa(watch.cmd.Process.Pid), "/bin/sh", "-c",
		`while [ ! -f "$1" ]; do sleep 0.02; done; exit 17`, "sh", finish)
	f.until(func() bool { return f.count("set") > 0 })
	_ = watch.cmd.Process.Kill()
	f.closed(1)
	if !j.running() {
		t.Fatal("watched PID exit terminated the command")
	}
	if err := os.WriteFile(finish, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if code := j.wait(t); code != 17 {
		t.Fatalf("exit=%d", code)
	}
}

func TestOverlappingJobs(t *testing.T) {
	f := newFixture(t)
	a, b := f.start("/bin/sleep", "30"), f.start("/bin/sleep", "30")
	f.until(func() bool { return f.count("open") == 2 && f.count("set") >= 4 })
	_ = a.cmd.Process.Kill()
	a.wait(t)
	f.closed(1)
	if f.count("clear") != 0 || !b.running() {
		t.Fatal("cleanup ran with another active member")
	}
	_ = b.cmd.Process.Kill()
	b.wait(t)
	f.closed(2)
	if f.count("clear") < 1 {
		t.Fatal("final cleanup missing")
	}
}

func TestSimultaneousExit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 16; i++ {
		f.start("/bin/sleep", "30")
	}
	f.until(func() bool { return f.setters() == 16 })
	for _, j := range f.jobs {
		_ = j.cmd.Process.Kill()
	}
	for _, j := range f.jobs {
		j.wait(t)
	}
	f.closed(16)
	if f.count("clear") < 1 {
		t.Fatal("no departing guardian restored sleep")
	}
}

func TestGroupSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			f := newFixture(t)
			marker := filepath.Join(f.dir, "ready")
			f.env = append(f.env, "DOUBLESHOT_TEST_SIGNAL_READY="+marker)
			j := f.start(helperBinary)
			f.until(func() bool { _, err := os.Stat(marker); return err == nil })
			if err := syscall.Kill(-j.cmd.Process.Pid, sig); err != nil {
				t.Fatal(err)
			}
			j.wait(t)
			status := j.cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !status.Signaled() || status.Signal() != sig {
				t.Fatalf("status=%v", status)
			}
			f.closed(1)
		})
	}
}

func TestArmingFailure(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_FAIL_SET=1")
	marker := filepath.Join(f.dir, "must-not-run")
	j := f.start("/usr/bin/touch", marker)
	if code := j.wait(t); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	f.closed(1)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command ran after arming failure")
	}
}

func TestExecFailures(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			f := newFixture(t)
			command := filepath.Join(f.dir, "command")
			want := 127
			if !missing {
				if err := os.WriteFile(command, []byte("not executable"), 0600); err != nil {
					t.Fatal(err)
				}
				want = 126
			}
			if code := f.start(command).wait(t); code != want {
				t.Fatalf("exit=%d", code)
			}
			if len(f.events()) != 0 {
				t.Fatal("power changed for an invalid command")
			}
		})
	}
}

func TestNoLockInheritance(t *testing.T) {
	f := newFixture(t)
	j := f.start("/bin/sh", "-c", "sleep 30 >/dev/null 2>&1 & exit 0")
	if code := j.wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	f.closed(1)
	lock, err := openMembership(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	f.until(func() bool { return flock(lock, syscall.LOCK_EX|syscall.LOCK_NB) == nil })
}

func TestJoinDuringCleanup(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_RESTORE_DELAY=250ms")
	a := f.start("/usr/bin/true")
	f.until(func() bool { return f.count("restore-start") == 1 })
	b := f.start("/bin/sleep", "30")
	f.until(func() bool { return f.setters() == 2 })
	// The newcomer must not enable protection before the previous cleanup
	// clears it. Each recorded operation also verifies actual flock ownership.
	cleared := false
	events := f.events()
	for _, e := range events {
		if e.Op == "clear" {
			cleared = true
		}
		if e.Op == "set" && e.PID != events[0].PID && !cleared {
			t.Fatal("newcomer joined before exclusive cleanup finished")
		}
	}
	if code := a.wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if !b.running() {
		t.Fatal("newcomer did not remain protected")
	}
	_ = b.cmd.Process.Kill()
	b.wait(t)
	f.closed(2)
}

func TestRestoreFailureReported(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_FAIL_RESTORE=3")
	j := f.start("/usr/bin/true")
	if code := j.wait(t); code != 0 {
		t.Fatalf("command exit changed: %d", code)
	}
	f.closed(1)
	if f.count("restore-failed") != 3 || !strings.Contains(j.err.String(), "cleanup failed") {
		t.Fatalf("missing cleanup diagnostic: events=%v stderr=%s", f.events(), j.err.String())
	}
}

func TestHelpAndInvalidOptionsDoNotTouchPower(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"-h"}, 0}, {[]string{"-i"}, 2}, {[]string{"-t", "bad"}, 2}, {[]string{"-w", "0"}, 2},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			f := newFixture(t)
			j := f.start(test.args...)
			if code := j.wait(t); code != test.code {
				t.Fatalf("exit=%d", code)
			}
			if test.code == 0 && !strings.Contains(j.out.String(), "Usage: doubleshot") {
				t.Fatal("missing help")
			}
			if len(f.events()) != 0 {
				t.Fatal("help/argument validation changed power")
			}
		})
	}
}

func TestMissingWatchedPID(t *testing.T) {
	f := newFixture(t)
	// Darwin PIDs fit in a signed 32-bit integer; this valid but unused value
	// exercises completion before any assertion or lid override is acquired.
	if code := f.start("-w", "2147483647").wait(t); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if len(f.events()) != 0 {
		t.Fatal("armed for a process that was already gone")
	}
}

func TestNoArguments(t *testing.T) {
	f := newFixture(t)
	j := f.start()
	f.until(func() bool { return f.count("set") >= 2 })
	if !j.running() {
		t.Fatal("unlimited protection exited early")
	}
	_ = j.cmd.Process.Signal(syscall.SIGTERM)
	j.wait(t)
	f.closed(1)
	if f.count("idle-acquire") != 1 || f.count("idle-release") != 1 {
		t.Fatalf("idle lifecycle: %v", f.events())
	}
}

func TestIdleAssertionFailure(t *testing.T) {
	f := newFixture(t)
	f.env = append(f.env, "DOUBLESHOT_TEST_FAIL_IDLE=1")
	marker := filepath.Join(f.dir, "must-not-run")
	if code := f.start("/usr/bin/touch", marker).wait(t); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	f.closed(1)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("command ran without idle protection")
	}
	if f.count("set") != 0 || f.count("idle-acquire") != 0 {
		t.Fatalf("partial acquisition: %v", f.events())
	}
}
