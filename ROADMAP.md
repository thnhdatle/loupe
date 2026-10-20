# Roadmap

Loupe v0.1 does one job: always-on tracing, cost and latency for LLM
applications from one binary and one SQLite file. The items below are explicit
non-goals for v0.1. Items marked `[gfi]` are suitable as good first issues.
Nothing here is a promise or a date.

## Non-goals for v0.1

- Evals and LLM-as-judge
- Datasets
- Prompt management
- Playground
- Annotation queues
- Alerts
- User scores and feedback
- Metrics storage (OTLP metrics are accepted, counted and discarded)
- Non-GenAI log storage [gfi]
- Postgres or ClickHouse storage
- Multi-node deployment
- RBAC and SSO
- Time to first token
- Sampling
- Arbitrary attribute search
- Project and tenant separation
- Exporting Loupe's own traces: `self_tracing.endpoint` is accepted and validated in the configuration, but v0.1 sends nothing
- Redaction speed: the regex rules cost about 270 us per span with content capture on (about half of the CPU in a profile of the retention test)
- Windows support: file locking and free-disk checks are implemented for Unix only
- Downgrading a database to an older binary (restore the `.pre-v<N>` copy instead)
- Authentication on the admin listener (`/metrics`, `/readyz`); keep it on a trusted network

## Items from `TODO(roadmap)` comments

Entries are added here when code carries a `TODO(roadmap)` marker; there are
none in v0.1.
