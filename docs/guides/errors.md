# Error registry

Every error returned by the Loupe API and CLI carries a stable code. Codes are
never reused or renamed.

| Code | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Missing or invalid bearer token. |
| `not_found` | 404 | Unknown endpoint, trace, session or job. |
| `method_not_allowed` | 405 | The endpoint does not support the method; `Allow` lists the supported ones. |
| `invalid_parameter` | 400 | A query parameter or JSON field is malformed, unknown or repeated. |
| `invalid_cursor` | 400 | The cursor is malformed or was issued for another sort or order. |
| `invalid_limit` | 400 | `limit` is not an integer in 1..500. |
| `invalid_range` | 400 | `from` is not before `to` (or `since` is after `until`), or the range is too large. |
| `invalid_sort` | 400 | Unknown `sort` or `order`. |
| `invalid_filter` | 400 | A filter value is invalid, for example an unknown `kind`, `status`, `group_by` or `interval`. |
| `invalid_id` | 400 | A trace id is not 32 hexadecimal characters. |
| `uri_too_long` | 414 | The query string exceeds the limit (8 KiB). |
| `body_too_large` | 413 | The request body exceeds the limit (4 KiB). |
| `too_many_jobs` | 429 | The admin job registry is full of unfinished jobs. |
| `shutting_down` | 503 | The server is shutting down and accepts no new jobs. |
| `internal` | 500 | Unexpected server error; see the log entry with the same `request_id`. |
