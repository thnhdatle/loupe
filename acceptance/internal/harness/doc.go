// SPDX-License-Identifier: Apache-2.0

// Package harness is the shared helper of the acceptance tests.
//
// It loads thresholds from acceptance/criteria.yaml, selects the Mac or CI
// values from the CI environment variable and writes the per-criterion log
// acceptance/logs/<ID>.json read by the acceptance runner.
package harness
