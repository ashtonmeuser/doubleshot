//go:build darwin && arm64

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func flock(file *os.File, operation int) error {
	for {
		err := syscall.Flock(int(file.Fd()), operation)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func openMembership(base string) (*os.File, error) {
	return membershipFile(base, true)
}

// The status path opens only existing files; it never creates directories,
// acquires membership, or takes an exclusive lock away from a departing member.
func membershipFile(base string, create bool) (*os.File, error) {
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if create {
		if err := root.Mkdir("doubleshot", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := root.Lstat("doubleshot")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return nil, errors.New("lock directory has unexpected type, owner or permissions")
	}
	directory, err := root.OpenRoot("doubleshot")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// Open existing first, then create exclusively. Retry concurrent creation;
	// never unlink the file, since every invocation must use the same inode.
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if !create {
		flags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	}
	var file *os.File
	for attempt := 0; attempt < 8; attempt++ {
		file, err = directory.OpenFile("members.lock", flags, 0600)
		if create && errors.Is(err, os.ErrNotExist) {
			file, err = directory.OpenFile("members.lock", flags|os.O_CREATE|os.O_EXCL, 0600)
		}
		if err == nil {
			break
		}
		if !create {
			return nil, err
		}
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err == nil {
		stat := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Getuid()) || stat.Nlink != 1 {
			err = errors.New("lock file has unexpected type, owner, permissions or links")
		}
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("validate membership lock: %w", err)
	}
	return file, nil
}
