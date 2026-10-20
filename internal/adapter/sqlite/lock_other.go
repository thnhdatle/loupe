// SPDX-License-Identifier: Apache-2.0

//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package sqlite

import (
	"errors"
	"fmt"
	"os"
)

var errUnsupportedOS = errors.New("sqlite: flock and statfs are not supported on this platform")

func acquireLock(path string) (*os.File, error) {
	return nil, fmt.Errorf("%w: %s", errUnsupportedOS, path)
}

func releaseLock(f *os.File) error { return f.Close() }

func freeDiskBytes(path string) (int64, error) {
	return 0, fmt.Errorf("%w: %s", errUnsupportedOS, path)
}
