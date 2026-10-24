// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var exportDay = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func seedTraces(t *testing.T, st *sqlite.Store, days ...int) {
	t.Helper()
	spans := make([]trace.Span, 0, len(days))
	for i, d := range days {
		var tid trace.ID
		tid[0], tid[15] = 0xab, byte(i+1)
		var sid trace.SpanID
		sid[0], sid[7] = 0xcd, byte(i+1)
		start := exportDay.AddDate(0, 0, d).UnixNano()
		spans = append(spans, trace.Span{
			TraceID: tid, SpanID: sid, ResourceID: 7, Service: "svc", Name: "op", Kind: trace.KindLLM,
			Status: trace.StatusOK, StartNs: start, EndNs: start + int64(time.Second), Attributes: []byte(`{}`),
		})
	}
	_, err := st.WriteBatch(context.Background(), &trace.Batch{
		Resources: []trace.Resource{{ID: 7, Attributes: []byte(`{"service.name":"svc"}`)}}, Spans: spans,
	})
	require.NoError(t, err)
}

func countLines(s string) int {
	n := 0
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		n++
	}
	return n
}

func TestExport_OnlineRangeFilter(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	st, err := sqlite.Open(context.Background(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	seedTraces(t, st, -2, 0, 2)

	out, err := runCmd(t, "--config", cfg, "export", "--from", "2026-03-01", "--to", "2026-04-01")
	require.NoError(t, err)
	assert.Equal(t, 3, countLines(out))

	out, err = runCmd(t, "--config", cfg, "export", "--from", "2026-03-10", "--to", "2026-03-11")
	require.NoError(t, err)
	assert.Equal(t, 1, countLines(out))
	assert.Contains(t, out, `"spans":[`)

	out, err = runCmd(t, "--config", cfg, "export", "--from", "2025-01-01", "--to", "2025-01-02")
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestExport_OutFileRefusesOverwrite(t *testing.T) {
	t.Parallel()
	cfg, dbPath := newDB(t)
	st, err := sqlite.Open(context.Background(), dbPath)
	require.NoError(t, err)
	seedTraces(t, st, 0)
	require.NoError(t, st.Close())

	dst := filepath.Join(t.TempDir(), "out.jsonl")
	_, err = runCmd(t, "--config", cfg, "export", "--from", "2026-03-10", "--to", "2026-03-11", "--out", dst)
	require.NoError(t, err)
	b, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, 1, countLines(string(b)))

	_, err = runCmd(t, "--config", cfg, "export", "--from", "2026-03-10", "--to", "2026-03-11", "--out", dst)
	require.ErrorIs(t, err, os.ErrExist)
	b2, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, b, b2, "existing file is untouched")
}

func TestExport_PendingMigrationRejected(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "loupe.db")
	_, err := sqlite.OpenReadOnly(context.Background(), dbPath)
	require.Error(t, err)

	cfg := writeCfgYAML(t, "storage:\n  path: \""+dbPath+"\"\n")
	_, err = runCmd(t, "--config", cfg, "export")
	require.Error(t, err)
}
