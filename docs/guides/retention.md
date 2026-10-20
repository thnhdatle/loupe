# Retention and disk cap

Loupe keeps data for a fixed time and keeps the database under a size cap. Both
are enforced by one retention planner inside `loupe serve`. This guide covers
what is deleted and when, how the disk cap behaves, and how to size the disk.
For deleting one user's data see [privacy.md](privacy.md#deleting-a-users-data);
for manual vacuum see [operations.md](operations.md).

## Settings

| Key | Default | Meaning |
|---|---|---|
| `retention.spans` | 14d | Age at which spans are deleted. |
| `retention.content` | 7d | Age at which stored content is deleted. Must not exceed `retention.spans`. |
| `retention.orphan_content` | 1h | Age at which content rows without a stored span are deleted. |
| `retention.rollups_hourly` | 35d | Age of hourly rollups. Must not exceed `retention.rollups_daily`. |
| `retention.rollups_daily` | 400d | Age of daily rollups and ingest stats. |
| `retention.interval` | 10m | Time between passes. |
| `retention.batch` | 5000 | Rows per delete job. |
| `retention.max_db_bytes` | 10 GiB | Disk cap on main file + WAL + SHM. `0` turns it off; the smallest non-zero value is 64 MiB. |

Config validation rejects `content > spans`, `rollups_hourly > rollups_daily`,
non-positive durations, a `batch` below 1 and a cap between 1 byte and 64 MiB.

## What a pass deletes

A pass handles these classes in this order:

1. `content`: content rows older than `retention.content`, by `time_ns` (the
   span start or event time).
2. `orphan_content`: content rows with no stored span, aged by their insert
   time (`retention.orphan_content`).
3. `spans`: spans older than `retention.spans`. Their content goes with them,
   and so do traces left without spans.
4. `sessions`: sessions that no longer have any trace.
5. `rollups_hourly`: `rollup_hour` and `rollup_user_hour`.
6. `rollups_daily`: `rollup_day`, `rollup_user_day` and `ingest_stats`.

Totals of a trace or session that straddles the span cutoff are not adjusted:
a trace with some spans deleted keeps its original totals until its last span
goes.

## How a pass runs

- A pass runs at startup and then every `retention.interval`.
- Each class is deleted in writer jobs of `retention.batch` rows, one job after
  another until a job deletes fewer rows than the batch. Ingest batches run
  between the jobs, so retention does not block ingest for long.
- Then incremental vacuum runs in jobs of 2048 pages (8 MiB) until the free list
  is empty or stops shrinking (ingest keeps freeing a few pages).
- Then one `wal_checkpoint(TRUNCATE)` job runs. If readers keep it from
  finishing, the WAL is left for the next pass and
  `loupe_retention_checkpoint_busy_total` is incremented.
- A failed step is logged and counted in `loupe_retention_pass_errors_total`;
  the other steps still run, and the next pass retries.

SQLite settings that affect the WAL: `wal_autocheckpoint` is 1000 pages and
`journal_size_limit` is 64 MiB.

## Disk cap

`retention.max_db_bytes` limits main file + WAL + SHM. After the age rules and
compaction, a pass measures the size. If it is above the cap, Loupe deletes the
oldest content first, then the oldest spans, one batch at a time, compacting and
re-measuring after each batch until the size is under the cap. Each such pass is
logged at warn and counted in `loupe_retention_cap_exceeded_total`.

- **Content goes first and goes fast.** Under cap pressure content is kept far
  shorter than `retention.content`. In the retention test roughly the last hour of
  content survived. If you
  need content for days, raise the cap or reduce `content.capture`.
- **The last hour is never deleted.** The cap never removes spans or content
  younger than one hour. If the database is still above the cap with only that
  left, Loupe logs an error, increments `loupe_retention_cap_unmet_total` and
  keeps ingesting. Raise the cap or lower the ingest volume.
- **A busy checkpoint ends the cap phase** for that pass: the size cannot drop
  until the WAL is checkpointed, and deleting further would overshoot.
- **The cap is enforced only at passes.** The database can exceed the cap by
  about one interval of ingest plus the WAL. The retention test measured a
  1.75% overshoot at a 200 MiB cap.
- At startup Loupe warns when free disk is below twice the cap, because
  compaction and the WAL need room beyond the cap.

## Sizing

Measured in the retention test of the acceptance suite (`acceptance/README.md`)
on a synthetic workload (your spans and content will differ):

- About 1274 bytes per stored span with content capture on. The synthetic
  content was about 2.5 KB raw per span that had content, compressed about 2:1
  with zstd.
- About 640 bytes per span once content has been trimmed by the cap.

Rough budget: spans per day x retention days x bytes per span, plus rollups
(small next to spans). Content is the part to watch; with `content.capture: off`
(the default) the figure is the smaller one.

## Metrics

| Metric | Labels | Meaning |
|---|---|---|
| `loupe_retention_deleted_total` | `class` | Rows deleted by age rules. |
| `loupe_retention_cap_deleted_total` | `class` | Rows deleted by the disk cap (`content`, `spans`). |
| `loupe_retention_passes_total` | | Passes completed. |
| `loupe_retention_pass_errors_total` | | Passes that failed. |
| `loupe_retention_cap_exceeded_total` | | Passes that found the database above the cap. |
| `loupe_retention_cap_unmet_total` | | Passes that hit the cap with only the last hour left. |
| `loupe_retention_checkpoint_busy_total` | | Checkpoints that could not finish. |
| `loupe_db_size_bytes` | | Main file + WAL + SHM at the last measurement (taken at passes when the cap is on). |
