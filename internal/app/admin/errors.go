// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"errors"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var (
	ErrEmptyUserID  = errors.New("admin: empty user id")
	ErrInvalidPath  = errors.New("admin: invalid backup path")
	ErrInvalidRange = errors.New("admin: since is after until")
	ErrClosed       = errors.New("admin: service closed")
	ErrTooManyJobs  = errors.New("admin: too many unfinished jobs")
	ErrJobNotFound  = fmt.Errorf("admin: job not found: %w", trace.ErrNotFound)
)
