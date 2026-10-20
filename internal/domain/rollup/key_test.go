// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func TestHourStartDayStart(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 10, 19, 13, 45, 12, 7, time.UTC)
	cases := []struct {
		name string
		ns   int64
		hour int64
		day  int64
	}{
		{
			name: "afternoon", ns: ts.UnixNano(),
			hour: time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC).UnixNano(),
			day:  time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).UnixNano(),
		},
		{name: "epoch", ns: 0, hour: 0, day: 0},
		{name: "exact hour", ns: 3 * rollup.HourNs, hour: 3 * rollup.HourNs, day: 0},
		{name: "before epoch", ns: -1, hour: -rollup.HourNs, day: -rollup.DayNs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.hour, rollup.HourStart(tc.ns))
			assert.Equal(t, tc.day, rollup.DayStart(tc.ns))
			assert.Equal(t, tc.hour, rollup.ResolutionHour.Start(tc.ns))
			assert.Equal(t, tc.day, rollup.ResolutionDay.Start(tc.ns))
		})
	}
}

func TestResolution_StringAndDuration(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "hour", rollup.ResolutionHour.String())
	assert.Equal(t, "day", rollup.ResolutionDay.String())
	assert.Equal(t, int64(time.Hour), rollup.ResolutionHour.Duration())
	assert.Equal(t, int64(24*time.Hour), rollup.ResolutionDay.Duration())
}

func TestKeyOf(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 19, 13, 45, 0, 0, time.UTC).UnixNano()
	s := trace.Span{StartNs: start, Service: "svc", Provider: "openai", Model: "gpt", Kind: trace.KindLLM}
	hour := rollup.KeyOf(&s, rollup.ResolutionHour)
	day := rollup.KeyOf(&s, rollup.ResolutionDay)
	assert.Equal(t, rollup.Key{
		BucketNs: rollup.HourStart(start), Service: "svc", Provider: "openai", Model: "gpt", Kind: trace.KindLLM,
	}, hour)
	assert.Equal(t, rollup.DayStart(start), day.Bucket())
	uk := rollup.UserKeyOf("u1", start, rollup.ResolutionDay)
	assert.Equal(t, rollup.UserKey{BucketNs: rollup.DayStart(start), UserID: "u1"}, uk)
	assert.Equal(t, rollup.DayStart(start), uk.Bucket())
}
