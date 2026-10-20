// SPDX-License-Identifier: Apache-2.0

// Package runner runs the acceptance suite behind `make acceptance`.
//
// It builds the project, brings up the compose stack, probes the `requires`
// entries of every criterion, executes each criterion test, reads the logs the
// tests write and generates acceptance/report.md.
package runner
