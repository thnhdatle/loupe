# Retention and disk cap

Choose how long Loupe keeps data and how large the database may grow.

To delete one user's data, see [privacy.md](privacy.md#deleting-a-users-data).
For manual vacuum, see [operations.md](operations.md).

## Settings

```yaml
retention:
  spans: 14d          # delete spans older than this
  content: 7d         # delete content older than this
  max_db_bytes: 10GiB # disk cap; 0 = off
```

| Key | Default | Meaning |
|---|---|---|
| `retention.spans` | 14d | Span age limit. |
| `retention.content` | 7d | Content age limit; not above `retention.spans`. |
| `retention.orphan_content` | 1h | Age limit for content without a span. |
| `retention.rollups_hourly` | 35d | Hourly rollup age; not above `retention.rollups_daily`. |
| `retention.rollups_daily` | 400d | Daily rollup and ingest stats age. |
| `retention.interval` | 10m | Time between passes. |
| `retention.batch` | 5000 | Rows per delete job. |
| `retention.max_db_bytes` | 10 GiB | Cap on main file + WAL + SHM. `0` = off; minimum 64 MiB. |

A pass runs at startup and then every `retention.interval`. Ingest continues during a pass.

## Disk cap

When the database exceeds `retention.max_db_bytes`, Loupe deletes the oldest content, then the
oldest spans, until it is under the cap. Each time it logs a warning.

- **Content goes first.** Under cap pressure, content may last only about an hour.
  To keep it for days, raise the cap or reduce `content.capture`.
- **The last hour is never deleted.** If that is still above the cap, Loupe logs an error and keeps
  ingesting. Raise the cap or send less.
- **The cap is checked only at passes.** The database can exceed it by about one interval of ingest plus the WAL.
- **Leave free disk.** Startup warns when free disk is below twice the cap.

## Sizing

Measured on a synthetic workload; yours will differ:

| Case | Bytes per span |
|---|---|
| content capture on | about 1274 |
| content trimmed by the cap, or capture off | about 640 |

Budget: spans per day x retention days x bytes per span. Rollups are small next to spans.

## Metrics

| Metric | Labels | Meaning |
|---|---|---|
| `loupe_retention_deleted_total` | `class` | Rows deleted by age. |
| `loupe_retention_cap_deleted_total` | `class` | Rows deleted by the cap (`content`, `spans`). |
| `loupe_retention_passes_total` | | Passes completed. |
| `loupe_retention_pass_errors_total` | | Failed passes. |
| `loupe_retention_cap_exceeded_total` | | Passes above the cap. |
| `loupe_retention_cap_unmet_total` | | Passes above the cap with one hour left. |
| `loupe_retention_checkpoint_busy_total` | | Checkpoints that could not finish. |
| `loupe_db_size_bytes` | | Main file + WAL + SHM size. |
