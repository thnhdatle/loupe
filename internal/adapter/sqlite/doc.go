// SPDX-License-Identifier: Apache-2.0

// Package sqlite implements the storage ports on SQLite in WAL mode, with the
// pure-Go driver modernc.org/sqlite (no cgo). The schema and its indexes are
// defined in migrations/; TestSchema_QueryPlans pins the index each query uses.
//
// Open takes an exclusive flock on <path>.lock (one process owns a database),
// sets and verifies the pragmas (auto_vacuum=INCREMENTAL from creation, WAL,
// synchronous, busy_timeout, foreign_keys, …), applies the embedded goose
// migrations, after a VACUUM INTO <path>.pre-v<N> backup when the database
// already has a schema, and opens a read-only (mode=ro, query_only) pool next
// to the single writer connection.
//
// Store implements trace.SpanStore (WriteBatch: one transaction per batch,
// INSERT OR IGNORE on (trace_id, span_id), aggregates and usage-dedupe deltas
// only for inserted spans), query.TraceReader, query.SessionReader,
// query.StatsReader, rollup.RollupReader, retention.Store and admin.Backuper;
// Store.Admin returns the admin.Store. Write methods must only be called from
// the single writer goroutine (app/writer); readers are safe for concurrent use.
//
// Usage:
//
//	store, err := sqlite.Open(ctx, cfg.Storage.Path, sqlite.WithClock(clk))
//	if err != nil {
//		return err
//	}
//	defer store.Close()
//	w, err := writer.New(store, clk)
//	adm := admin.New(store.Admin(), store, w, clk)
package sqlite
