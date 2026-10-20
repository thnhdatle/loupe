// SPDX-License-Identifier: Apache-2.0

package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/adapter/sqlite"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// budgetLog is a GenAI log event part of span (1, span) at time n.
func budgetLog(span int, n int64, body string) trace.ContentPart {
	return trace.ContentPart{
		TraceID: tid(1), SpanID: sid(span), Source: trace.SourceLog, Key: "gen_ai.user.message",
		Role: "user", Bytes: len(body), Body: []byte(body), TimeNs: 1_000 + n,
	}
}

// budgetBodies returns the stored bodies (decompressed) and truncated flags of
// span (1, span) in insert order; a marker body is reported as "<nil>".
func budgetBodies(t *testing.T, path string, span int) ([]string, []bool) {
	t.Helper()
	dec, err := zstd.NewReader(nil)
	require.NoError(t, err)
	defer dec.Close()
	tr, sp := tid(1), sid(span)
	rows, err := rawDB(t, path).QueryContext(context.Background(),
		"SELECT body, truncated FROM span_content WHERE trace_id = ? AND span_id = ? ORDER BY id", tr[:], sp[:])
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var (
		bodies []string
		truncs []bool
	)
	for rows.Next() {
		var (
			blob  []byte
			trunc bool
		)
		require.NoError(t, rows.Scan(&blob, &trunc))
		body := "<nil>"
		if blob != nil {
			b, err := dec.DecodeAll(blob, nil)
			require.NoError(t, err)
			body = string(b)
		}
		bodies, truncs = append(bodies, body), append(truncs, trunc)
	}
	require.NoError(t, rows.Err())
	return bodies, truncs
}

func TestStore_ContentBudget_SharedWithinBatch(t *testing.T) {
	t.Parallel()
	s := openStore(t, sqlite.WithContentMaxBytes(100))
	sp := mkSpan(1, 2, 0, trace.KindLLM)
	sp.Content = []trace.ContentPart{{
		Source: trace.SourceAttribute, Key: "input.value", Role: "user", Bytes: 60, Body: []byte(strings.Repeat("s", 60)),
	}}
	_, err := s.WriteBatch(context.Background(), &trace.Batch{
		Spans: []trace.Span{sp},
		Content: []trace.ContentPart{
			budgetLog(2, 1, strings.Repeat("a", 30)),
			budgetLog(3, 1, strings.Repeat("o", 100)), // another span: its own budget
			budgetLog(2, 2, strings.Repeat("b", 30)),
			budgetLog(2, 3, "c"),
		},
	})
	require.NoError(t, err)

	bodies, truncs := budgetBodies(t, s.Path(), 2)
	assert.Equal(t, []string{strings.Repeat("s", 60), strings.Repeat("a", 30), strings.Repeat("b", 10), ""}, bodies)
	assert.Equal(t, []bool{false, false, true, true}, truncs)
	bodies, truncs = budgetBodies(t, s.Path(), 3)
	assert.Equal(t, []string{strings.Repeat("o", 100)}, bodies)
	assert.Equal(t, []bool{false}, truncs)
}

// TestStore_ContentBudget_PersistsAcrossReopen: the budget is derived from the
// stored rows, so it survives a restart; a rune is never split and a
// truncated span stays exhausted even when a small gap is left.
func TestStore_ContentBudget_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	path := dbPath(t)
	ctx := context.Background()
	s := openAt(t, path, sqlite.WithContentMaxBytes(100))
	_, err := s.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{budgetLog(2, 1, strings.Repeat("a", 90))}})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	s = openAt(t, path, sqlite.WithContentMaxBytes(100))
	euro := strings.Repeat("€", 7) // 21 bytes; 10 remain, cut at a rune boundary to 9
	_, err = s.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{budgetLog(2, 2, euro)}})
	require.NoError(t, err)
	_, err = s.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{budgetLog(2, 3, "z")}})
	require.NoError(t, err)

	bodies, truncs := budgetBodies(t, path, 2)
	assert.Equal(t, []string{strings.Repeat("a", 90), "€€€", ""}, bodies)
	assert.Equal(t, []bool{false, true, true}, truncs)
}

// TestStore_ContentBudget_MarkersAndNoLimit: markers (capture off,
// metadata_only) are stored as given and use no budget; zero means no limit.
func TestStore_ContentBudget_MarkersAndNoLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	limited := openStore(t, sqlite.WithContentMaxBytes(10))
	marker := budgetLog(2, 1, "")
	marker.Body, marker.Bytes = nil, 500
	_, err := limited.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{marker, budgetLog(2, 2, "0123456789")}})
	require.NoError(t, err)
	bodies, truncs := budgetBodies(t, limited.Path(), 2)
	assert.Equal(t, []string{"<nil>", "0123456789"}, bodies)
	assert.Equal(t, []bool{false, false}, truncs)

	unlimited := openStore(t)
	_, err = unlimited.WriteBatch(ctx, &trace.Batch{Content: []trace.ContentPart{
		budgetLog(2, 1, strings.Repeat("a", 50_000)), budgetLog(2, 2, strings.Repeat("b", 50_000)),
	}})
	require.NoError(t, err)
	_, truncs = budgetBodies(t, unlimited.Path(), 2)
	assert.Equal(t, []bool{false, false}, truncs)
}
