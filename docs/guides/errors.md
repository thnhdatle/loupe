# Error registry

Look up the error codes returned by the Loupe API and CLI. Codes are never reused or renamed.

| Code | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Missing or invalid bearer token. |
| `not_found` | 404 | Unknown endpoint, trace, session or job. |
| `method_not_allowed` | 405 | Method not supported; see `Allow`. |
| `invalid_parameter` | 400 | Malformed, unknown or repeated parameter or field. |
| `invalid_cursor` | 400 | Malformed cursor, or from another sort or order. |
| `invalid_limit` | 400 | `limit` not an integer in 1..500. |
| `invalid_range` | 400 | `from` not before `to` (or `since` after `until`), or range too large. |
| `invalid_sort` | 400 | Unknown `sort` or `order`. |
| `invalid_filter` | 400 | Invalid filter value, e.g. `kind`, `status`, `group_by`, `interval`. |
| `invalid_id` | 400 | Trace id not 32 hex characters. |
| `uri_too_long` | 414 | Query string over 8 KiB. |
| `body_too_large` | 413 | Request body over 4 KiB. |
| `too_many_jobs` | 429 | Admin job registry full. |
| `shutting_down` | 503 | Server shutting down; no new jobs. |
| `internal` | 500 | Server error; see the log entry with the same `request_id`. |
