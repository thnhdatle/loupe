//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/bench/load"
)

// Capacity step search: rates in spans/s, ascending, 2 min each (CI 30 s).
var lpe03Steps = []float64{2500, 3000, 5000, 7500, 10000, 15000, 20000}

const (
	lpe03StepMac     = 2 * time.Minute
	lpe03StepCI      = 30 * time.Second
	lpe03Overload    = 3 // overload rate = 3 × capacity
	lpe03GRPCShare   = 0.10
	lpe03Seed        = 3
	lpe03StepRunBase = 100 // run ids of the steps: 101..107
	lpe03ModeRunBase = 200 // run ids of the overload modes: 201, 202
)

// TestAcceptance_LPE03_Overload is the acceptance test of criterion LPE-03.
//
// Step search: every step runs on a fresh server and a fresh database (deleted
// afterwards), so that each rate is measured from the same starting point
// rather than on a database grown by the earlier steps; the load mix is the
// overload mix (90% OTLP/HTTP, 10% OTLP/gRPC, overflow reject). Every step
// runs; capacity is the highest step with 0 rejected and 0 dropped, where a
// step also counts as failed if any request got no response, an unexpected
// error, was skipped by the generator's in-flight cap, or a responded span
// was not stored.
//
// Overload: for each overflow mode (reject, drop) a fresh server and database
// with default limits receives 3 × capacity for minutes_per_mode. Gates per
// mode: peak RSS (sampled every 1 s from start to exit, including the drain)
// < peak_rss_mib_max; unaccounted = sent − (stored + rejected + dropped) = 0
// where stored counts the database rows of responded requests (spans of
// requests without a response are not "sent" and are subtracted by
// their sequence number, so a transport error is reported but cannot hide or
// fake a loss); the client's stored/rejected/dropped equal /v1/ingest/stats
// and the Overview banner exactly (stats_mismatches); every gRPC
// RESOURCE_EXHAUSTED carries RetryInfo.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE03_Overload(t *testing.T) {
	run := harness.Start(t, "LPE-03")
	g := lpe03Gates{
		rssMax:         perfNum(run, "peak_rss_mib_max"),
		maxUnaccounted: int64(perfNum(run, "unaccounted_spans")),
		maxMismatch:    int(perfNum(run, "stats_mismatches")),
		maxNoRetryInfo: int64(perfNum(run, "grpc_rejections_without_retryinfo")),
	}
	minutes := perfNum(run, "minutes_per_mode")
	modes := int(perfNum(run, "modes"))
	stepDur := lpe03StepMac
	if run.CI {
		stepDur = lpe03StepCI
	}
	measured := map[string]any{"step_seconds": stepDur.Seconds(), "minutes_per_mode": minutes}

	capacity, steps, records := lpe03StepSearch(t, run.ID, stepDur)
	measured["steps"] = steps
	measured["capacity_spans_per_s"] = capacity
	if capacity == 0 {
		if err := perfAppendResults(run.ID, records...); err != nil {
			t.Logf("append %s: %v", perfResultsFile, err)
		}
		run.Fail(measured, "step search: no step passed with 0 rejected and 0 dropped")
		return
	}
	over := lpe03Overload * capacity
	measured["overload_spans_per_s"] = over
	records = append(records, map[string]any{
		"name": "capacity", "capacity_spans_per_s": capacity, "overload_spans_per_s": over,
	})

	var (
		fails      []string
		peakRSS    float64
		unaccTotal int64
		mismatches int
		noInfo     int64
	)
	for i, mode := range []string{load.ModeReject, load.ModeDrop}[:modes] {
		name := "overload_" + mode
		res, err := perfRun(t, run.ID, perfConfig{
			Name: name, Rate: over, Duration: time.Duration(minutes * float64(time.Minute)), Mode: mode,
			GRPCFraction: lpe03GRPCShare, RunID: uint32(lpe03ModeRunBase + i + 1), Seed: lpe03Seed,
		})
		sum := perfSummary(res)
		if err != nil {
			sum["error"] = err.Error()
			fails = append(fails, fmt.Sprintf("%s: %v", name, err))
		}
		unacc := lpe03Unaccounted(res)
		mm := lpe03Mismatches(res)
		sum["unaccounted_spans"] = unacc
		sum["stats_mismatches"] = mm
		measured[name] = sum
		records = append(records, map[string]any{"name": name, "summary": sum})
		peakRSS = max(peakRSS, res.PeakRSSMiB)
		unaccTotal += lpe03Abs(unacc)
		mismatches += len(mm)
		noInfo += res.Load.GRPCRejectionsWithoutRetryInfo
		fails = append(fails, g.check(name, res, over)...)
	}
	measured["peak_rss_mib"] = peakRSS
	measured["unaccounted_spans"] = unaccTotal
	measured["stats_mismatches"] = mismatches
	measured["grpc_rejections_without_retryinfo"] = noInfo
	if err := perfAppendResults(run.ID, records...); err != nil {
		t.Logf("append %s: %v", perfResultsFile, err)
	}
	if len(fails) > 0 {
		run.Fail(measured, "%s", strings.Join(fails, "; "))
		return
	}
	run.Pass(measured, fmt.Sprintf("capacity %.0f spans/s; overload %.0f spans/s: peak RSS %.1f MiB, "+
		"0 unaccounted, stats and banner equal, every gRPC rejection with RetryInfo", capacity, over, peakRSS))
}

// lpe03StepSearch runs every step and returns the capacity (0 if no step
// passed), the per-step summaries and the records for perf-results.jsonl.
func lpe03StepSearch(t *testing.T, id string, stepDur time.Duration) (float64, []map[string]any, []any) {
	t.Helper()
	var capacity float64
	steps := make([]map[string]any, 0, len(lpe03Steps))
	records := make([]any, 0, len(lpe03Steps)+3)
	for i, rate := range lpe03Steps {
		name := fmt.Sprintf("step_%.0f", rate)
		res, err := perfRun(t, id, perfConfig{
			Name: name, Rate: rate, Duration: stepDur, Mode: load.ModeReject, GRPCFraction: lpe03GRPCShare,
			RunID: uint32(lpe03StepRunBase + i + 1), Seed: lpe03Seed,
		})
		sum := perfSummary(res)
		ok := err == nil && lpe03StepOK(res)
		sum["ok"] = ok
		if err != nil {
			sum["error"] = err.Error()
		}
		steps = append(steps, sum)
		records = append(records, map[string]any{"name": name, "summary": sum})
		if ok {
			capacity = rate
		}
	}
	return capacity, steps, records
}

// lpe03Gates are the thresholds of one overload mode.
type lpe03Gates struct {
	rssMax         float64
	maxUnaccounted int64
	maxMismatch    int
	maxNoRetryInfo int64
}

// check returns the gate failures of one overload mode.
func (g lpe03Gates) check(name string, res perfResult, over float64) []string {
	var fails []string
	add := func(format string, args ...any) { fails = append(fails, name+": "+fmt.Sprintf(format, args...)) }
	if res.PeakRSSMiB >= g.rssMax {
		add("peak RSS %.1f MiB >= %.0f", res.PeakRSSMiB, g.rssMax)
	}
	if u := lpe03Unaccounted(res); lpe03Abs(u) > g.maxUnaccounted {
		add("unaccounted %d = sent %d − (stored %d + rejected %d + dropped %d)", u, res.Load.SentSpans,
			res.storedOfSent(), res.Load.RejectedSpans, res.Load.DroppedSpans)
	}
	if mm := lpe03Mismatches(res); len(mm) > g.maxMismatch {
		add("stats mismatches: %s", strings.Join(mm, ", "))
	}
	if n := res.Load.GRPCRejectionsWithoutRetryInfo; n > g.maxNoRetryInfo {
		add("%d gRPC rejections without RetryInfo", n)
	}
	if res.Load.RejectedSpans+res.Load.DroppedSpans == 0 {
		add("nothing rejected or dropped at %.0f spans/s: not an overload", over)
	}
	if res.StopError != "" {
		add("server stop: %s", res.StopError)
	}
	return fails
}

// lpe03Unaccounted is sent − (stored + rejected + dropped) over responded requests.
func lpe03Unaccounted(r perfResult) int64 {
	return r.Load.SentSpans - (r.storedOfSent() + r.Load.RejectedSpans + r.Load.DroppedSpans)
}

// lpe03StepOK reports whether a step ran at its rate without loss of any kind.
func lpe03StepOK(r perfResult) bool {
	l := r.Load
	return l.RejectedSpans == 0 && l.DroppedSpans == 0 && l.TransportErrors == 0 && l.OtherErrorSpans == 0 &&
		l.SkippedBacklog == 0 && l.Unfinished == 0 && r.DBSpans == l.SentSpans && r.StopError == ""
}

// lpe03Mismatches compares the client's counts with /v1/ingest/stats and the
// Overview banner. Each differing number is one mismatch.
func lpe03Mismatches(r perfResult) []string {
	mm := []string{}
	eq := func(what string, client, server int64) {
		if client != server {
			mm = append(mm, fmt.Sprintf("%s client %d != server %d", what, client, server))
		}
	}
	l := r.Load
	eq("stored (db) vs stats.stored", r.DBSpans, r.Stats.Stored)
	eq("stored of sent vs stats.stored", r.storedOfSent(), r.Stats.Stored)
	eq("rejected vs stats.rejected", l.RejectedSpans, r.Stats.rejected())
	eq("dropped vs stats.dropped", l.DroppedSpans, r.Stats.dropped())
	eq("rejected vs banner", l.RejectedSpans, r.Banner.Rejected)
	eq("dropped vs banner", l.DroppedSpans, r.Banner.Dropped)
	if (l.RejectedSpans+l.DroppedSpans > 0) != r.Banner.Shown {
		mm = append(mm, fmt.Sprintf("banner shown=%t with %d rejected and %d dropped", r.Banner.Shown,
			l.RejectedSpans, l.DroppedSpans))
	}
	// Every span the server received was stored, rejected or dropped (no
	// duplicates: span ids are unique per run).
	eq("stats.received vs stored+rejected+dropped", r.Stats.Received,
		r.Stats.Stored+r.Stats.Duplicates+r.Stats.rejected()+r.Stats.dropped())
	return mm
}

func lpe03Abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
