// SPDX-License-Identifier: Apache-2.0

package retention_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/retention"
)

func TestConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(c *retention.Config)
		want   []string // substrings, one per expected error; empty = valid
	}{
		{name: "defaults", mutate: func(*retention.Config) {}},
		{name: "cap off", mutate: func(c *retention.Config) { c.MaxDBBytes = 0 }},
		{name: "cap at minimum", mutate: func(c *retention.Config) { c.MaxDBBytes = retention.MinMaxDBBytes }},
		{name: "content equals spans", mutate: func(c *retention.Config) { c.Content = c.Spans }},
		{
			name:   "content above spans",
			mutate: func(c *retention.Config) { c.Content = c.Spans + time.Hour },
			want:   []string{"retention.content"},
		},
		{
			name:   "hourly above daily",
			mutate: func(c *retention.Config) { c.RollupsHourly = c.RollupsDaily + time.Hour },
			want:   []string{"retention.rollups_hourly"},
		},
		{
			name:   "cap below minimum",
			mutate: func(c *retention.Config) { c.MaxDBBytes = retention.MinMaxDBBytes - 1 },
			want:   []string{"retention.max_db_bytes"},
		},
		{
			name:   "negative cap",
			mutate: func(c *retention.Config) { c.MaxDBBytes = -1 },
			want:   []string{"retention.max_db_bytes"},
		},
		{name: "zero batch", mutate: func(c *retention.Config) { c.Batch = 0 }, want: []string{"retention.batch"}},
		{
			name: "every duration zero",
			mutate: func(c *retention.Config) {
				*c = retention.Config{MaxDBBytes: 0, Batch: 1}
			},
			want: []string{
				"retention.spans must", "retention.content must", "retention.rollups_hourly must",
				"retention.rollups_daily must", "retention.orphan_content must", "retention.interval must",
			},
		},
		{
			name: "all errors at once",
			mutate: func(c *retention.Config) {
				c.Content = c.Spans * 2
				c.RollupsHourly = c.RollupsDaily * 2
				c.MaxDBBytes = 1
				c.Batch = -1
				c.Interval = -time.Second
			},
			want: []string{
				"retention.content", "retention.rollups_hourly", "retention.max_db_bytes",
				"retention.batch", "retention.interval",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := retention.DefaultConfig()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if len(tc.want) == 0 {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, retention.ErrInvalidConfig)
			lines := strings.Split(err.Error(), "\n")
			assert.Len(t, lines, len(tc.want))
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

func TestDefaultConfig_MatchesDocs(t *testing.T) {
	t.Parallel()
	const day = 24 * time.Hour
	assert.Equal(t, retention.Config{
		Spans:         14 * day,
		Content:       7 * day,
		RollupsHourly: 35 * day,
		RollupsDaily:  400 * day,
		OrphanContent: time.Hour,
		MaxDBBytes:    10 << 30,
		Interval:      10 * time.Minute,
		Batch:         5000,
	}, retention.DefaultConfig())
}
