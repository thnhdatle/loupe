// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// params reads and validates the query string of one request. The first
// malformed value is kept in err; later reads become no-ops.
type params struct {
	v   url.Values
	err error
}

// newParams checks that the query holds only allowed names and no repeats.
func newParams(v url.Values, allowed ...string) (*params, error) {
	for name, vals := range v {
		if !slices.Contains(allowed, name) {
			return nil, &paramError{fmt.Sprintf("unknown query parameter %q", name)}
		}
		if len(vals) > 1 {
			return nil, &paramError{fmt.Sprintf("query parameter %q given more than once", name)}
		}
	}
	return &params{v: v}, nil
}

func (p *params) fail(format string, args ...any) {
	if p.err == nil {
		p.err = &paramError{fmt.Sprintf(format, args...)}
	}
}

func (p *params) str(name string) string { return p.v.Get(name) }

func (p *params) time(name string) time.Time {
	s := p.v.Get(name)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		p.fail("%s must be an RFC 3339 timestamp", name)
	}
	return t
}

func (p *params) boolean(name string) (value, set bool) {
	s := p.v.Get(name)
	if s == "" {
		return false, false
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		p.fail("%s must be true or false", name)
		return false, false
	}
	return b, true
}

func (p *params) hasError() *bool {
	if b, ok := p.boolean("has_error"); ok {
		return &b
	}
	return nil
}

// limit reads the page limit; range checks are left to the use case.
func (p *params) limit() int {
	s := p.v.Get("limit")
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1 // reported as query.ErrInvalidLimit by the use case
	}
	return n
}

func (p *params) page() query.Page {
	return query.Page{Limit: p.limit(), Cursor: p.v.Get("cursor")}
}

// minCost parses min_cost in USD into nano-USD.
func (p *params) minCost() *int64 {
	s := p.v.Get("min_cost")
	if s == "" {
		return nil
	}
	n, err := query.ParseUSD(s)
	if err != nil {
		p.err = err
		return nil
	}
	return &n
}

// minDuration parses min_duration_ms into nanoseconds.
func (p *params) minDuration() int64 {
	s := p.v.Get("min_duration_ms")
	if s == "" {
		return 0
	}
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms < 0 || ms > math.MaxInt64/int64(time.Millisecond) {
		p.fail("min_duration_ms must be a non-negative integer")
		return 0
	}
	return ms * int64(time.Millisecond)
}

func (p *params) spanFilter() query.SpanFilter {
	return query.SpanFilter{
		Service:       p.str("service"),
		Model:         p.str("model"),
		Provider:      p.str("provider"),
		Kind:          trace.Kind(p.str("kind")),
		Status:        trace.Status(strings.ToLower(p.str("status"))),
		MinDurationNs: p.minDuration(),
		NamePrefix:    p.str("name"),
	}
}
