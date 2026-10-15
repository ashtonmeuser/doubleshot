//go:build darwin && arm64

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"time"
)

const usage = `Usage: doubleshot [-t seconds] [-w pid] [--] [command [args...]]
       doubleshot -s

Prevent idle sleep and lid-close sleep while protection is active.

  -t seconds  Stop protection after this many positive whole seconds.
  -w pid      Stop protection when this process exits.
  -s          Print status without changing power settings.
  -h          Show this help without changing power settings.

Options must precede the command. Use -- to end doubleshot's options.
Protection ends when the command exits, the watched PID exits, or the timeout
expires, whichever happens first. Expiry never terminates the command.
Without a command or limit, run until interrupted.
Status (-s) cannot be combined with a command, -t, or -w.
`

type options struct {
	Timeout time.Duration
	WaitPID int
	Command []string
	Help    bool
	Status  bool
}

func parseOptions(args []string) (options, error) {
	var result options
	var seconds int64
	flags := flag.NewFlagSet("doubleshot", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Int64Var(&seconds, "t", 0, "timeout in seconds")
	flags.IntVar(&result.WaitPID, "w", 0, "watched PID")
	flags.BoolVar(&result.Help, "h", false, "help")
	flags.BoolVar(&result.Status, "s", false, "status")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			result.Help = true
			return result, nil
		}
		return result, err
	}
	if result.Help {
		return result, nil
	}
	var invalid error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "t":
			if seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
				invalid = fmt.Errorf("-t must be a positive whole number of seconds within time.Duration range")
			}
		case "w":
			if result.WaitPID <= 0 || result.WaitPID > math.MaxInt32 {
				invalid = fmt.Errorf("-w must be a positive process ID")
			}
		}
	})
	if invalid == nil {
		result.Timeout = time.Duration(seconds) * time.Second
	}
	result.Command = flags.Args()
	if result.Status && (result.Timeout != 0 || result.WaitPID != 0 || len(result.Command) > 0) {
		return result, errors.New("-s cannot be combined with a command, -t, or -w")
	}
	return result, invalid
}
