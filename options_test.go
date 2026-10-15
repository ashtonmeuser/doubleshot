//go:build darwin && arm64

package main

import (
	"reflect"
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	for _, test := range []struct {
		args    []string
		command []string
		timeout time.Duration
		pid     int
	}{
		{[]string{"-t", "123", "somecommand"}, []string{"somecommand"}, 123 * time.Second, 0},
		{[]string{"-t", "123", "--", "somecommand"}, []string{"somecommand"}, 123 * time.Second, 0},
		{[]string{"somecommand"}, []string{"somecommand"}, 0, 0},
		{[]string{"--", "somecommand"}, []string{"somecommand"}, 0, 0},
		{[]string{"-w", "123"}, []string{}, 0, 123},
		{[]string{"-t=2", "-w=123", "command", "-h", "", "a b"}, []string{"command", "-h", "", "a b"}, 2 * time.Second, 123},
		{[]string{"--", "-command"}, []string{"-command"}, 0, 0},
	} {
		t.Run(test.args[0], func(t *testing.T) {
			got, err := parseOptions(test.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Command, test.command) || got.Timeout != test.timeout || got.WaitPID != test.pid {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"-i"}, {"-d"}, {"-u"}, {"-m"}, {"-t"}, {"-w"},
		{"-t", "0"}, {"-t", "-1"}, {"-t", "1.5"}, {"-t", "1s"}, {"-t", "9223372036854775807"},
		{"-s", "-t", "1"}, {"-s", "-w", "123"}, {"-s", "command"},
		{"-w", "0"}, {"-w", "-1"}, {"-w", "2147483648"}, {"-w", "not-a-pid"},
	} {
		t.Run(args[0], func(t *testing.T) {
			if _, err := parseOptions(args); err == nil {
				t.Fatalf("accepted %v", args)
			}
		})
	}
}

func TestStatusOption(t *testing.T) {
	got, err := parseOptions([]string{"-s"})
	if err != nil || !got.Status {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = parseOptions([]string{"command", "-s"})
	if err != nil || got.Status || !reflect.DeepEqual(got.Command, []string{"command", "-s"}) {
		t.Fatalf("got %+v, %v", got, err)
	}
}
