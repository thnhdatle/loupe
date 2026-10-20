// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"net/url"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
)

// formTimeLayout is what <input type="datetime-local"> submits; read as UTC.
const formTimeLayout = "2006-01-02T15:04"

// hourlyMax is the longest range charted by the hour; longer ones use days.
const hourlyMax = 7 * 24 * time.Hour

type preset struct {
	Key   string
	Label string
	Span  time.Duration
}

var presets = []preset{
	{"1h", "1 h", time.Hour},
	{"6h", "6 h", 6 * time.Hour},
	{"24h", "24 h", 24 * time.Hour},
	{"7d", "7 d", 7 * 24 * time.Hour},
	{"30d", "30 d", 30 * 24 * time.Hour},
}

// presetLink is one entry of the time-range picker.
type presetLink struct {
	Key     string
	Label   string
	Current bool
}

// rangeView is the resolved time range of a page and what its picker shows.
type rangeView struct {
	From, To   time.Time
	Key        string // preset key, or "custom"
	Presets    []presetLink
	CustomFrom string
	CustomTo   string
	Interval   query.Interval
}

// resolveRange reads "range" (a preset) or "from" and "to" (UTC, datetime-local
// or RFC 3339) from the query.
func resolveRange(q url.Values, now time.Time) (rangeView, error) {
	from, to := q.Get("from"), q.Get("to")
	var rv rangeView
	if from != "" || to != "" {
		f, t, err := parseCustom(from, to, now)
		if err != nil {
			return rangeView{}, err
		}
		rv = rangeView{From: f, To: t, Key: "custom", CustomFrom: from, CustomTo: to}
	} else {
		rv = presetRange(q.Get("range"), now)
	}
	rv.Interval = query.IntervalHour
	if rv.To.Sub(rv.From) > hourlyMax {
		rv.Interval = query.IntervalDay
	}
	for _, p := range presets {
		rv.Presets = append(rv.Presets, presetLink{Key: p.Key, Label: p.Label, Current: p.Key == rv.Key})
	}
	return rv, nil
}

// presetRange resolves a preset key; an unknown key means the default.
func presetRange(key string, now time.Time) rangeView {
	span := 24 * time.Hour
	for _, p := range presets {
		if p.Key == key {
			span = p.Span
		}
	}
	for _, p := range presets {
		if p.Span == span {
			key = p.Key
		}
	}
	return rangeView{From: now.Add(-span), To: now, Key: key}
}

func parseCustom(from, to string, now time.Time) (time.Time, time.Time, error) {
	f, err := parseOptionalTime(from)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from: %w", query.ErrInvalidRange, err)
	}
	t, err := parseOptionalTime(to)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: to: %w", query.ErrInvalidRange, err)
	}
	switch {
	case f.IsZero() && t.IsZero():
	case t.IsZero():
		t = now
	case f.IsZero():
		f = t.Add(-24 * time.Hour)
	}
	if !f.Before(t) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: from is not before to", query.ErrInvalidRange)
	}
	return f, t, nil
}

// parseOptionalTime parses RFC 3339 or datetime-local; empty yields the zero time.
func parseOptionalTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(formTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("unrecognised time %q, use RFC 3339", s)
	}
	return t, nil
}
