// SPDX-License-Identifier: Apache-2.0

package runner

import (
	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
)

// Outcome is the final state of one criterion row.
type Outcome string

// Row outcomes.
const (
	OutcomePass    Outcome = "PASS"
	OutcomeFail    Outcome = "FAIL"
	OutcomeSkipped Outcome = "Skipped (environment)"
	OutcomeBlocked Outcome = "Blocked (environment)"
)

// Result is one row of the acceptance report.
type Result struct {
	Criterion criteria.Criterion
	Outcome   Outcome
	Measured  string
	Threshold string
	// Detail is the failure cause or the probe result behind a skip or block.
	Detail string
	// Requirement is the unmet requirement of a skipped or blocked row.
	Requirement string
}

// LogPath returns the repository-relative path of the criterion text log.
func LogPath(id string) string {
	return "acceptance/logs/" + id + ".txt"
}
