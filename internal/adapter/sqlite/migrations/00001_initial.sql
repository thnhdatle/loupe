-- SPDX-License-Identifier: Apache-2.0
-- Initial schema. Query plans are pinned by TestSchema_QueryPlans.

-- +goose Up
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;

INSERT INTO meta (key, value) VALUES ('schema', 'loupe');

-- id is the uint64 hash of the canonical attributes JSON, stored bit-cast to int64.
CREATE TABLE resources (
  id         INTEGER PRIMARY KEY,
  attributes TEXT NOT NULL
);

-- One row per stored span. cost_nanos is the span's own cost (NULL = unpriced);
-- eff_* hold the span's current contribution after usage dedupe (cache and
-- reasoning counts and cost taken from duplicates), so later batches can apply
-- exact deltas. rollup_user is the user the span was attributed to at write time.
CREATE TABLE spans (
  id                 INTEGER PRIMARY KEY,
  trace_id           BLOB    NOT NULL,
  span_id            BLOB    NOT NULL,
  parent_span_id     BLOB,
  resource_id        INTEGER NOT NULL,
  service            TEXT    NOT NULL,
  name               TEXT    NOT NULL,
  kind               TEXT    NOT NULL,
  status             TEXT    NOT NULL,
  start_ns           INTEGER NOT NULL,
  end_ns             INTEGER NOT NULL,
  duration_ns        INTEGER NOT NULL,
  model              TEXT    NOT NULL,
  provider           TEXT    NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  has_usage          INTEGER NOT NULL,
  cost_nanos         INTEGER,
  session_id         TEXT    NOT NULL,
  user_id            TEXT    NOT NULL,
  error_type         TEXT    NOT NULL,
  flags              INTEGER NOT NULL,
  content_stripped   INTEGER NOT NULL,
  rollup_user        TEXT    NOT NULL,
  eff_cache_read     INTEGER NOT NULL,
  eff_cache_write    INTEGER NOT NULL,
  eff_reasoning      INTEGER NOT NULL,
  eff_cost_nanos     INTEGER,
  attributes         TEXT    NOT NULL,
  UNIQUE (trace_id, span_id)
);
CREATE INDEX spans_start ON spans (start_ns);
CREATE INDEX spans_user ON spans (user_id) WHERE user_id <> '';

-- Content parts and markers (body NULL). No foreign key to spans: log events
-- may precede their span. has_span is 0 while no span (trace_id, span_id) is
-- stored; the orphan rule uses it with inserted_ns (store clock).
CREATE TABLE span_content (
  id          INTEGER PRIMARY KEY,
  trace_id    BLOB    NOT NULL,
  span_id     BLOB    NOT NULL,
  source      TEXT    NOT NULL,
  time_ns     INTEGER NOT NULL,
  key         TEXT    NOT NULL,
  part        INTEGER NOT NULL,
  role        TEXT    NOT NULL,
  bytes       INTEGER NOT NULL,
  body        BLOB,
  truncated   INTEGER NOT NULL,
  has_span    INTEGER NOT NULL,
  inserted_ns INTEGER NOT NULL,
  UNIQUE (trace_id, span_id, source, time_ns, key, part)
);
CREATE INDEX span_content_time ON span_content (time_ns);
CREATE INDEX span_content_orphan ON span_content (inserted_ns) WHERE has_span = 0;

-- Incremental trace totals; token, cost and unpriced sums exclude usage_duplicate spans.
CREATE TABLE traces (
  trace_id           BLOB PRIMARY KEY,
  root_name          TEXT,
  root_service       TEXT    NOT NULL,
  start_ns           INTEGER NOT NULL,
  end_ns             INTEGER NOT NULL,
  duration_ns        INTEGER NOT NULL,
  span_count         INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL,
  unpriced           INTEGER NOT NULL,
  models             TEXT    NOT NULL,
  has_error          INTEGER NOT NULL,
  session_id         TEXT    NOT NULL,
  user_id            TEXT    NOT NULL
) WITHOUT ROWID;
CREATE INDEX traces_start ON traces (start_ns);
CREATE INDEX traces_cost ON traces (cost_nanos);
CREATE INDEX traces_duration ON traces (duration_ns);
CREATE INDEX traces_session ON traces (session_id, start_ns) WHERE session_id <> '';
CREATE INDEX traces_user ON traces (user_id) WHERE user_id <> '';

CREATE TABLE sessions (
  id                 TEXT PRIMARY KEY,
  user_id            TEXT    NOT NULL,
  first_ns           INTEGER NOT NULL,
  last_ns            INTEGER NOT NULL,
  trace_count        INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX sessions_last ON sessions (last_ns);
CREATE INDEX sessions_user ON sessions (user_id, last_ns) WHERE user_id <> '';

-- Rollups: measures exclude usage_duplicate spans, the histogram covers all spans.
CREATE TABLE rollup_hour (
  bucket_ns          INTEGER NOT NULL,
  service            TEXT    NOT NULL,
  provider           TEXT    NOT NULL,
  model              TEXT    NOT NULL,
  kind               TEXT    NOT NULL,
  span_count         INTEGER NOT NULL,
  errors             INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL,
  unpriced           INTEGER NOT NULL,
  histogram          BLOB    NOT NULL,
  PRIMARY KEY (bucket_ns, service, provider, model, kind)
) WITHOUT ROWID;

CREATE TABLE rollup_day (
  bucket_ns          INTEGER NOT NULL,
  service            TEXT    NOT NULL,
  provider           TEXT    NOT NULL,
  model              TEXT    NOT NULL,
  kind               TEXT    NOT NULL,
  span_count         INTEGER NOT NULL,
  errors             INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL,
  unpriced           INTEGER NOT NULL,
  histogram          BLOB    NOT NULL,
  PRIMARY KEY (bucket_ns, service, provider, model, kind)
) WITHOUT ROWID;

CREATE TABLE rollup_user_hour (
  bucket_ns          INTEGER NOT NULL,
  user_id            TEXT    NOT NULL,
  span_count         INTEGER NOT NULL,
  errors             INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL,
  unpriced           INTEGER NOT NULL,
  histogram          BLOB    NOT NULL,
  PRIMARY KEY (bucket_ns, user_id)
) WITHOUT ROWID;
CREATE INDEX rollup_user_hour_user ON rollup_user_hour (user_id);

CREATE TABLE rollup_user_day (
  bucket_ns          INTEGER NOT NULL,
  user_id            TEXT    NOT NULL,
  span_count         INTEGER NOT NULL,
  errors             INTEGER NOT NULL,
  input_tokens       INTEGER NOT NULL,
  output_tokens      INTEGER NOT NULL,
  cache_read_tokens  INTEGER NOT NULL,
  cache_write_tokens INTEGER NOT NULL,
  reasoning_tokens   INTEGER NOT NULL,
  cost_nanos         INTEGER NOT NULL,
  unpriced           INTEGER NOT NULL,
  histogram          BLOB    NOT NULL,
  PRIMARY KEY (bucket_ns, user_id)
) WITHOUT ROWID;
CREATE INDEX rollup_user_day_user ON rollup_user_day (user_id);

-- Receiver and writer counters folded per UTC hour.
CREATE TABLE ingest_stats (
  hour_ns INTEGER NOT NULL,
  signal  TEXT    NOT NULL,
  result  TEXT    NOT NULL,
  reason  TEXT    NOT NULL,
  n       INTEGER NOT NULL,
  PRIMARY KEY (hour_ns, signal, result, reason)
) WITHOUT ROWID;

-- +goose Down
-- Downgrades are not supported (operations.md); restore a .pre-v<N> backup instead.
SELECT 1;
