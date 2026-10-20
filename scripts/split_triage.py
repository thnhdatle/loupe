# SPDX-License-Identifier: Apache-2.0
"""Splits captured OTLP/JSON trace requests into one file per trace.

usage: split_triage.py CAPTURE_DIR OUT_DIR

Every trace holds the example app's spans and Triage's spans. Traces are named after the
scenarios of examples/triage/app.py, in the order the app runs them.
"""

import glob
import json
import os
import sys

SCENARIOS = ["chat-single-attempt", "chat-fallback", "chat-stream-continuation", "embeddings-chain"]


def main(capture: str, out: str) -> None:
    traces: dict[str, list[dict]] = {}
    for path in sorted(glob.glob(os.path.join(capture, "r-traces-*.json"))):
        with open(path) as f:
            doc = json.load(f)
        for rs in doc["resourceSpans"]:
            by_trace: dict[str, list[dict]] = {}
            for ss in rs["scopeSpans"]:
                for span in ss["spans"]:
                    by_trace.setdefault(span["traceId"], []).append(span)
            for tid, spans in by_trace.items():
                scopes = [
                    {"scope": ss["scope"], "spans": [s for s in ss["spans"] if s["traceId"] == tid]}
                    for ss in rs["scopeSpans"]
                ]
                scopes = [s for s in scopes if s["spans"]]
                traces.setdefault(tid, []).append({"resource": rs["resource"], "scopeSpans": scopes})

    def start(tid: str) -> int:
        return min(int(s["startTimeUnixNano"]) for rs in traces[tid] for ss in rs["scopeSpans"] for s in ss["spans"])

    def from_app(tid: str) -> bool:
        # Triage also traces requests that carry no trace context (its readiness probe).
        attrs = [a for rs in traces[tid] for a in rs["resource"]["attributes"] if a["key"] == "service.name"]
        return any(a["value"]["stringValue"].startswith("example-triage") for a in attrs)

    ordered = sorted((t for t in traces if from_app(t)), key=start)
    if len(ordered) != len(SCENARIOS):
        sys.exit(f"split_triage: {len(ordered)} traces captured, want {len(SCENARIOS)}")
    for name, tid in zip(SCENARIOS, ordered):
        path = os.path.join(out, name + ".json")
        with open(path, "w") as f:
            json.dump({"resourceSpans": traces[tid]}, f, indent=2)
            f.write("\n")
        print(path)


main(sys.argv[1], sys.argv[2])
