// SPDX-License-Identifier: Apache-2.0

//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package sqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// acquireLock opens (creating it if needed) the lock file and takes an
// exclusive, non-blocking flock on it. The kernel releases the lock when the
// process exits for any reason, including kill -9.
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, path)
		}
		return nil, fmt.Errorf("sqlite: flock %s: %w", path, err)
	}
	return f, nil
}

// releaseLock drops the flock and closes the file. The file itself is kept:
// removing it could let two processes lock different inodes.
func releaseLock(f *os.File) error {
	errUnlock := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := errors.Join(errUnlock, f.Close()); err != nil {
		return fmt.Errorf("sqlite: release lock: %w", err)
	}
	return nil
}

// freeDiskBytes returns the bytes available to unprivileged users on the
// file system holding path.
func freeDiskBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("sqlite: statfs %s: %w", path, err)
	}
	return asInt64(st.Bavail) * asInt64(st.Bsize), nil
}

// asInt64 converts statfs fields, whose types differ between platforms.
func asInt64[T ~int32 | ~uint32 | ~int64 | ~uint64](v T) int64 {
	return int64(v)
}
