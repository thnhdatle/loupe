// SPDX-License-Identifier: Apache-2.0

// Package admin implements pricing recompute, vacuum, backup and user deletion.
//
// Writes run as bounded jobs on the single writer through a trace.JobRunner
// (each job one transaction of at most the batch size), followed by a
// checkpoint job. Backup runs on a read-only connection, outside the writer.
// Every operation is asynchronous: Service returns a job id at once and
// JobStatus reports progress. Main type: Service.
//
// Usage:
//
//	id, err := svc.DeleteUser("user-1")
//	st, err := svc.JobStatus(id)
//	err = svc.Close()
package admin
