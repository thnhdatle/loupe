// SPDX-License-Identifier: Apache-2.0

// Command exsummary polls Loupe's REST API for the trace of one example run and
// prints a deterministic summary that examples/*/expected.txt can be diffed against.
//
// A summary is printed once two reads 500 ms apart agree.
//
// Usage: exsummary [-min-spans N] [-content] SERVICE
//
// Environment: LOUPE_API (default http://127.0.0.1:3000), LOUPE_API_TOKEN (optional
// bearer token) and LOUPE_CAPTURE=on, set when Loupe runs with content.capture: on;
// -content then waits for and counts stored content parts.
package main
