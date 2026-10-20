// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportRange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	r, err := exportRange("", "", now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-24*time.Hour), r.From)
	assert.Equal(t, now, r.To)

	r, err = exportRange("2026-03-01", "2026-03-02T06:00:00Z", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), r.From)
	assert.Equal(t, time.Date(2026, 3, 2, 6, 0, 0, 0, time.UTC), r.To)

	_, err = exportRange("nope", "", now)
	require.ErrorIs(t, err, errUsage)
	_, err = exportRange("2026-03-02", "2026-03-01", now)
	require.ErrorIs(t, err, errUsage)
}

func TestExport_RejectsUnknownFormat(t *testing.T) {
	t.Parallel()
	_, _, err := runRoot(t, "export", "--format", "csv")
	require.ErrorIs(t, err, errUsage)
}
