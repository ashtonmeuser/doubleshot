# doubleshot

**Like `caffeinate`, with the lid closed.**

Keep your Apple Silicon Mac awake while a command runs—even with the lid closed
and no external monitor connected. A familiar CLI, no sudo, and no service to
install.

## Install

With [Homebrew](https://brew.sh) on an Apple Silicon Mac:

```sh
brew install ashtonmeuser/tap/doubleshot
```

## Usage

```sh
doubleshot ./long-running-job   # protect a command
doubleshot -t 600               # stay awake for 10 minutes
doubleshot -w 1234              # stay awake until a process exits
doubleshot -s                   # show power, display, lid, and session status
```

Supports a small subset of `caffeinate`'s interface: `-t seconds`, `-w pid`, and an
optional command. Options go before the command; `--` is supported. Protection
ends when the command exits, the watched PID exits, or the timeout expires,
whichever comes first. Expiry does not terminate the command.

With no arguments, stay awake until interrupted. Use `-h` for help. Unlike
`caffeinate`, `-s` means status and must be used on its own.

## How it works

Written in Go with a small C bridge to IOKit. A guardian process holds an idle
sleep assertion (exactly like `caffeinate`) and periodically reapplies a lid-sleep
override. Your command retains its PID, standard streams, and exit behavior.

Concurrent invocations coordinate through one lock file. The last guardian
releases protection and conditionally clears the lid override, preserving native
clamshell mode when AC power and an external monitor are present.

The lid override uses an undocumented API and is shared with macOS and other
apps, so exact restoration of preexisting overrides is not guaranteed. Killing
the final guardian with SIGKILL can leave the override enabled until reboot.

## Development

Requires an Apple Silicon Mac, Go 1.25+, and Xcode Command Line Tools.

```sh
make build
make test
make race
```

Tests use a fake power backend and do not change sleep settings.

Inspired by [coke](https://github.com/maltekliemann/coke) and
[Clamshell](https://clamshell.dev/guides/how-keep-awake-apps-work-lid-closed).
