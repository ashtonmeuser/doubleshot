//go:build darwin && arm64

package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const guardianFlag = "--internal-guardian"

type guardianConfig struct {
	Timeout time.Duration
	WaitPID int
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) > 0 && args[0] == guardianFlag {
		return runGuardian(args[1:])
	}
	opts, err := parseOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "doubleshot:", err)
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	if opts.Help {
		fmt.Print(usage)
		return 0
	}
	if opts.Status {
		if err := printStatus(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "doubleshot: status:", err)
			return 1
		}
		return 0
	}
	var path string
	if len(opts.Command) > 0 {
		path, err = exec.LookPath(opts.Command[0])
		if err != nil {
			return commandError(err)
		}
	}
	guardian, err := launchGuardian(guardianConfig{Timeout: opts.Timeout, WaitPID: opts.WaitPID})
	if err != nil {
		fmt.Fprintln(os.Stderr, "doubleshot:", err)
		return 1
	}
	if len(opts.Command) == 0 {
		if err := guardian.Wait(); err != nil {
			fmt.Fprintln(os.Stderr, "doubleshot: guardian:", err)
			return 1
		}
		return 0
	}
	// Replace the launcher directly with the command, retaining its PID,
	// standard streams, environment and native exit/signal behavior.
	_ = guardian.Process.Release()
	return commandError(syscall.Exec(path, opts.Command, os.Environ()))
}

func commandError(err error) int {
	fmt.Fprintln(os.Stderr, "doubleshot: execute command:", err)
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, exec.ErrNotFound) {
		return 127
	}
	return 126
}

func launchGuardian(config guardianConfig) (*exec.Cmd, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	cmd := exec.Command(executable, guardianFlag,
		strconv.FormatInt(int64(config.Timeout), 10), strconv.Itoa(config.WaitPID))
	cmd.ExtraFiles = []*os.File{write}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		write.Close()
		return nil, fmt.Errorf("start guardian: %w", err)
	}
	write.Close()
	failed := true
	defer func() {
		if failed {
			_ = cmd.Process.Release()
		}
	}()
	if err := read.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return nil, err
	}
	var reply [1]byte
	if _, err := io.ReadFull(read, reply[:]); err != nil {
		return nil, fmt.Errorf("guardian did not establish protection: %w", err)
	}
	// D means a watched process was already gone; no protection was needed.
	if reply[0] != 'R' && reply[0] != 'D' {
		return nil, errors.New("invalid guardian acknowledgement")
	}
	failed = false
	return cmd, nil
}

// This private entry point receives a readiness pipe on FD 3 from the launcher.
func runGuardian(args []string) int {
	signal.Ignore(syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM,
		syscall.SIGTSTP, syscall.SIGTTIN, syscall.SIGTTOU, syscall.SIGPIPE)
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "doubleshot: invalid guardian configuration")
		return 1
	}
	timeout, timeoutErr := strconv.ParseInt(args[0], 10, 64)
	pid, pidErr := strconv.Atoi(args[1])
	if timeoutErr != nil || pidErr != nil || timeout < 0 || pid < 0 || pid > math.MaxInt32 {
		fmt.Fprintln(os.Stderr, "doubleshot: invalid guardian configuration")
		return 1
	}
	ready := os.NewFile(3, "guardian-ready")
	defer ready.Close()
	if info, err := ready.Stat(); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		fmt.Fprintln(os.Stderr, "doubleshot: missing guardian readiness pipe")
		return 1
	}
	syscall.CloseOnExec(3)
	if err := guard(os.Getppid(), ready, guardianConfig{Timeout: time.Duration(timeout), WaitPID: pid}); err != nil {
		fmt.Fprintln(os.Stderr, "doubleshot:", err)
		return 1
	}
	return 0
}
