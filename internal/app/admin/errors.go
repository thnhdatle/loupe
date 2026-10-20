// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"errors"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var (
	// ErrEmptyUserID reports a user deletion without a user id.
	ErrEmptyUserID = errors.New("admin: empty user id")
	// ErrInvalidPath reports an empty backup path or one containing a NUL byte.
	ErrInvalidPath = errors.New("admin: invalid backup path")
	// ErrInvalidRange reports a recompute range whose since is after its until.
	ErrInvalidRange = errors.New("admin: since is after until")
	// ErrClosed reports an operation started after Close.
	ErrClosed = errors.New("admin: service closed")
	// ErrTooManyJobs reports that the job registry is full of unfinished jobs.
	ErrTooManyJobs = errors.New("admin: too many unfinished jobs")
	// ErrJobNotFound reports an unknown or evicted job id. It matches trace.ErrNotFound.
	ErrJobNotFound = fmt.Errorf("admin: job not found: %w", trace.ErrNotFound)
)
