// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Error codes of the API (registered in docs/guides/errors.md).
const (
	CodeUnauthorized     = "unauthorized"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInvalidParameter = "invalid_parameter"
	CodeInvalidCursor    = "invalid_cursor"
	CodeInvalidLimit     = "invalid_limit"
	CodeInvalidRange     = "invalid_range"
	CodeInvalidSort      = "invalid_sort"
	CodeInvalidFilter    = "invalid_filter"
	CodeInvalidID        = "invalid_id"
	CodeURITooLong       = "uri_too_long"
	CodeBodyTooLarge     = "body_too_large"
	CodeTooManyJobs      = "too_many_jobs"
	CodeShuttingDown     = "shutting_down"
	CodeInternal         = "internal"
)

// classify maps an error from the use cases to an HTTP status and stable code.
// Only client-caused errors carry their text; everything else is "internal".
func classify(err error) (status int, code string, expose bool) {
	table := []struct {
		target error
		status int
		code   string
	}{
		{query.ErrInvalidCursor, http.StatusBadRequest, CodeInvalidCursor},
		{query.ErrInvalidLimit, http.StatusBadRequest, CodeInvalidLimit},
		{query.ErrInvalidRange, http.StatusBadRequest, CodeInvalidRange},
		{query.ErrInvalidSort, http.StatusBadRequest, CodeInvalidSort},
		{query.ErrInvalidUSD, http.StatusBadRequest, CodeInvalidParameter},
		{query.ErrInvalidFilter, http.StatusBadRequest, CodeInvalidFilter},
		{trace.ErrInvalidID, http.StatusBadRequest, CodeInvalidID},
		{trace.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{admin.ErrEmptyUserID, http.StatusBadRequest, CodeInvalidParameter},
		{admin.ErrTooManyJobs, http.StatusTooManyRequests, CodeTooManyJobs},
		{admin.ErrClosed, http.StatusServiceUnavailable, CodeShuttingDown},
	}
	for _, e := range table {
		if errors.Is(err, e.target) {
			return e.status, e.code, e.status < http.StatusInternalServerError
		}
	}
	return http.StatusInternalServerError, CodeInternal, false
}

// paramError is a malformed query parameter.
type paramError struct{ msg string }

func (e *paramError) Error() string { return e.msg }
