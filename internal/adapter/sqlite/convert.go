// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// maxTraceModels caps traces.models (≤ 8). The kept models
// are the lexicographically smallest, so the set does not depend on arrival order.
const maxTraceModels = 8

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// costArg binds a cost: NULL when unpriced.
func costArg(c trace.Cost) any {
	if !c.Priced {
		return nil
	}
	return c.Nanos
}

func costOf(n sql.NullInt64) trace.Cost {
	if !n.Valid {
		return trace.Cost{}
	}
	return trace.Priced(n.Int64)
}

// parentArg binds a parent span id: NULL for a root.
func parentArg(p trace.SpanID) any {
	if p.IsZero() {
		return nil
	}
	return p[:]
}

func scanSpanID(b []byte) (trace.SpanID, error) {
	id, err := trace.SpanIDFromBytes(b)
	if err != nil {
		return trace.SpanID{}, fmt.Errorf("sqlite: stored span id: %w", err)
	}
	return id, nil
}

func scanTraceID(b []byte) (trace.ID, error) {
	id, err := trace.IDFromBytes(b)
	if err != nil {
		return trace.ID{}, fmt.Errorf("sqlite: stored trace id: %w", err)
	}
	return id, nil
}

// resourceKey stores a uint64 resource hash bit-cast into SQLite's int64.
func resourceKey(id uint64) int64 { return int64(id) } //nolint:gosec // G115: intentional bit cast

func encodeModels(models []string) string {
	if len(models) == 0 {
		return "[]"
	}
	b, err := json.Marshal(models)
	if err != nil {
		// Unreachable: a []string always marshals.
		return "[]"
	}
	return string(b)
}

func decodeModels(s string) ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("sqlite: stored models %q: %w", s, err)
	}
	return out, nil
}

// addModel inserts m into the sorted set, keeping at most maxTraceModels of
// the smallest values. It returns a new slice when the set changes.
func addModel(models []string, m string) []string {
	if m == "" {
		return models
	}
	i, found := slices.BinarySearch(models, m)
	if found || i >= maxTraceModels {
		return models
	}
	out := slices.Insert(slices.Clone(models), i, m)
	if len(out) > maxTraceModels {
		out = out[:maxTraceModels]
	}
	return out
}
