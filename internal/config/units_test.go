// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/config"
)

func TestParseDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "30s", want: 30 * time.Second},
		{in: "200ms", want: 200 * time.Millisecond},
		{in: "14d", want: 14 * 24 * time.Hour},
		{in: "1d12h", want: 36 * time.Hour},
		{in: "0", want: 0},
		{in: "10m", want: 10 * time.Minute},
		{in: "", wantErr: true},
		{in: "d", wantErr: true},
		{in: "-1s", wantErr: true},
		{in: "5", wantErr: true},
		{in: "1.5d", wantErr: true},
		{in: "99999999999d", wantErr: true},
		{in: "1dx", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := config.ParseDuration(tc.in)
			if tc.wantErr {
				require.ErrorIs(t, err, config.ErrInvalidDuration)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{in: "16MiB", want: 16 << 20},
		{in: "64KiB", want: 64 << 10},
		{in: "10GiB", want: 10 << 30},
		{in: "1TiB", want: 1 << 40},
		{in: "1024", want: 1024},
		{in: "7B", want: 7},
		{in: "0", want: 0},
		{in: "2mib", want: 2 << 20},
		{in: "", wantErr: true},
		{in: "MiB", wantErr: true},
		{in: "-1MiB", wantErr: true},
		{in: "1.5MiB", wantErr: true},
		{in: "1MB", wantErr: true},
		{in: "99999999999TiB", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := config.ParseSize(tc.in)
			if tc.wantErr {
				require.ErrorIs(t, err, config.ErrInvalidSize)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatRoundTrip(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, time.Second, 200 * time.Millisecond, 36 * time.Hour, 400 * 24 * time.Hour} {
		got, err := config.ParseDuration(config.FormatDuration(d))
		require.NoError(t, err)
		assert.Equal(t, d, got)
	}
	for _, n := range []int64{0, 1, 1023, 1024, 640 << 20, 10 << 30, 1<<20 + 1} {
		got, err := config.ParseSize(config.FormatSize(n))
		require.NoError(t, err)
		assert.Equal(t, n, got)
	}
	assert.Equal(t, "14d", config.Duration(14*24*time.Hour).String())
	assert.Equal(t, "640MiB", config.Size(640<<20).String())
}
