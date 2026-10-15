//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLockValidation(t *testing.T) {
	for _, kind := range []string{"directory permissions", "file permissions", "symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			directory := filepath.Join(base, "doubleshot")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "members.lock")
			var err error
			switch kind {
			case "directory permissions":
				err = os.Chmod(directory, 0755)
			case "file permissions":
				err = os.WriteFile(path, nil, 0644)
				if err == nil {
					err = os.Chmod(path, 0644)
				}
			case "symlink":
				err = os.Symlink("other.lock", path)
			case "hardlink":
				err = os.WriteFile(path, nil, 0600)
				if err == nil {
					err = os.Link(path, filepath.Join(directory, "other.lock"))
				}
			case "fifo":
				err = syscall.Mkfifo(path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			file, err := openMembership(base)
			if file != nil {
				file.Close()
			}
			if err == nil {
				t.Fatal("accepted unsafe lock")
			}
		})
	}
}
