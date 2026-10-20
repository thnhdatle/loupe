# Benchmark results

## Measured envelope

Loupe v0.1.0 pre-release build, measured on 2026-10-19 on the reference
machine: MacBook Pro M3 Pro (11 cores), 36 GB, macOS 26.1, arm64; native
`loupe serve` binary (Go 1.27.2, `CGO_ENABLED=0`), default config unless
stated. Command: `make acceptance ONLY=LPE-02,LPE-03,LPE-04,LPE-06` (the logs
in `acceptance/logs/` are copied to the untracked `out/bench/`), and the LPE-03 step search
again with `LOUPE_INGEST_SYNC_ACK=true` for the `sync_ack` table. LPE-02 is
3 runs; the other figures are one run each. The acceptance report
(`acceptance/report.md`) is a later full run of the same suite; its values
differ slightly (for example q2 p95 0.063 s, start to ready 0.027 s max) and
pass the same thresholds. Raw data is not published: rerunning the command writes
`LPE-0*.json` and `perf-results.jsonl` to `acceptance/logs/`. Load generator:
`bench/cmd/loadgen` with the LPE-02 payload (512 spans per request, 90%
OTLP/HTTP, 10% OTLP/gRPC, 10% of llm spans with 2–8 KiB content attributes,
`content.capture: off`).

### Summary

| Figure | Value | Source |
|---|---|---|
| Sustained ingest (LPE-02, 3 × 10 min) | 3,001 spans/s, 0 lost, peak RSS 213 MiB | LPE-02 |
| Stretch run (10 min) | 5,001 spans/s, 0 lost, peak RSS 213 MiB | LPE-02 |
| Single-writer capacity, `sync_ack: false` | 15,000 spans/s (highest clean step) | LPE-03 step search |
| Single-writer capacity, `sync_ack: true` | 15,000 spans/s (p50 latency ~150 ms vs ~10 ms) | `sync_ack` step search |
| Overload at 45,000 spans/s (3 × capacity) | accepted ~15,100 spans/s, rest rejected/dropped with 0 unaccounted, peak RSS 470 MiB | LPE-03 |
| Query p95 at 2M spans / 30 days (worst of 8 queries) | 0.058 s | LPE-04 |
| DB size at 2M spans / 30 days (`capture: on`) | 1.06 GB | LPE-04 |
| Bytes per million spans, LPE-04 dataset (`capture: on`) | ~531 MB | LPE-04 |
| Bytes per million spans, LPE-02 payload (`capture: off`, ~600 B attributes) | ~957 MB | LPE-02 |
| Start to ready (5 starts) / idle RSS after 10 min / image | 0.85 s max, 0.02 s median / 29.8 MiB / 23.3 MB | LPE-06 |

The LPE-02 payload is heavier per span than the generated LPE-04 dataset
(larger attribute JSON on every span), which is why its bytes per million spans
are higher even without content. Size a deployment with the figure whose
attributes resemble your traffic.

The 0.85 s start-to-ready maximum is the first start of a freshly built binary,
which includes the operating system's first-launch scan; the LPE-06 testruns the binary once before timing, and the acceptance report measures 0.027 s
max.

### Step search, `sync_ack: false` (default)

2 min per step, fresh DB, ascending (LPE-03). Capacity = highest step with
0 rejected and 0 dropped.

| Target spans/s | Sent spans/s | Rejected | Dropped | p50 ms | p99 ms | Peak RSS MiB | DB bytes |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 2,500 | 2,504 | 0 | 0 | 13.7 | 24.3 | 212.8 | 288,382,976 |
| 3,000 | 3,004 | 0 | 0 | 12.4 | 24.4 | 212.1 | 346,632,192 |
| 5,000 | 5,004 | 0 | 0 | 10.7 | 17.6 | 213.1 | 576,905,216 |
| 7,500 | 7,504 | 0 | 0 | 9.6 | 14.0 | 213.3 | 864,505,856 |
| 10,000 | 10,004 | 0 | 0 | 9.7 | 14.8 | 213.1 | 1,152,348,160 |
| 15,000 | 15,004 | 0 | 0 | 10.1 | 14.5 | 278.4 | 1,727,414,272 |
| 20,000 | 20,004 | 320,512 | 0 | 9.0 | 12.4 | 455.5 | 1,996,251,136 |

### Step search, `sync_ack: true`

Same steps and payload, `LOUPE_INGEST_SYNC_ACK=true` (each response waits for
the commit of its spans). RSS sampled every 1 s with `ps`.

| Target spans/s | Sent spans/s | Rejected | Dropped | p50 ms | p99 ms | Peak RSS MiB | DB bytes |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 2,500 | 2,504 | 0 | 0 | 148.6 | 260.2 | 210.2 | 287,657,984 |
| 3,000 | 3,004 | 0 | 0 | 155.2 | 273.4 | 211.2 | 345,788,416 |
| 5,000 | 5,004 | 0 | 0 | 164.5 | 272.3 | 213.0 | 575,311,872 |
| 7,500 | 7,504 | 0 | 0 | 189.7 | 312.9 | 214.8 | 862,453,760 |
| 10,000 | 10,004 | 0 | 0 | 154.5 | 254.0 | 213.0 | 1,149,431,808 |
| 15,000 | 15,004 | 0 | 0 | 156.8 | 242.3 | 213.9 | 1,722,892,288 |
| 20,000 | 20,003 | 237,568 | 0 | 1528.5 | 5065.6 | 391.0 | 2,070,102,016 |

`sync_ack` does not lower the single-writer capacity (15,000 spans/s in both modes):
throughput is bound by the writer's commit rate, not by when the response is sent.
It raises request latency from ~10 ms to ~150–190 ms p50 (~250–310 ms p99), because
each response waits for the batch commit; clients need enough concurrent requests
(here up to 4,096 in flight) to sustain the rate.

### LPE-02 runs (3,000 spans/s, 10 min each)

| Run | Sent spans/s | Lost | p99 ms | Peak RSS MiB | DB bytes | Spans |
|---|---:|---:|---:|---:|---:|---:|
| 1 | 3,001 | 0 | 20.8 | 212.6 | 1,723,486,208 | 1,800,192 |
| 2 | 3,001 | 0 | 21.3 | 213.1 | 1,723,359,232 | 1,800,192 |
| 3 | 3,001 | 0 | 21.5 | 211.1 | 1,723,359,232 | 1,800,192 |
| 5k stretch | 5,001 | 0 | 16.3 | 213.0 | 2,872,770,560 | 3,000,320 |

### LPE-04 query latency (2M spans, 100k traces, 30 days; 30 runs after 3 warm-ups)

| Query | p50 s | p95 s | max s |
|---|---:|---:|---:|
| q1 traces, last 24 h | 0.0005 | 0.0010 | 0.0010 |
| q2 traces, 7 d, model + error | 0.0537 | 0.0580 | 0.0582 |
| q3 traces, 7 d, service + min duration | 0.0165 | 0.0227 | 0.0231 |
| q4 traces, 30 d, by cost | 0.0004 | 0.0005 | 0.0007 |
| q5 session of 200 traces | 0.0009 | 0.0014 | 0.0015 |
| q6 trace of 40 spans with content | 0.0005 | 0.0007 | 0.0007 |
| q7 overview, 30 d, by model and day | 0.0357 | 0.0382 | 0.0390 |
| q8 traces, 7 d, name prefix | 0.0005 | 0.0007 | 0.0008 |

## Go micro-benchmarks

Generated by `make bench` (`scripts/bench.sh`); only the section between the
markers is rewritten.

<!-- BEGIN GENERATED: go-benchmarks -->
<!-- END GENERATED: go-benchmarks -->
