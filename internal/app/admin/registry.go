// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"sync"
	"time"
)

// State is the lifecycle state of an admin job.
type State string

// Job states.
const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// Kind names an admin operation.
type Kind string

// Job kinds.
const (
	KindRecompute  Kind = "pricing_recompute"
	KindVacuum     Kind = "vacuum"
	KindBackup     Kind = "backup"
	KindUserDelete Kind = "user_delete"
)

// JobStatus is a snapshot of an admin job. It never contains user data.
type JobStatus struct {
	ID    string
	Kind  Kind
	State State
	// Rows counts rows processed so far (spans repriced, rows deleted, pages freed).
	Rows int
	// Error is the failure text when State is StateFailed.
	Error      string
	CreatedAt  time.Time
	StartedAt  time.Time // zero while pending
	FinishedAt time.Time // zero until done or failed
}

// registry keeps the most recent jobs, bounded by max. mu protects every field.
type registry struct {
	mu    sync.Mutex
	max   int
	jobs  map[string]*JobStatus
	order []string // oldest first
}

func newRegistry(maxJobs int) *registry {
	return &registry{max: maxJobs, jobs: make(map[string]*JobStatus)}
}

// add registers a pending job, evicting the oldest finished job when full.
func (r *registry) add(st JobStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.order) >= r.max && !r.evictLocked() {
		return ErrTooManyJobs
	}
	r.jobs[st.ID] = &st
	r.order = append(r.order, st.ID)
	return nil
}

func (r *registry) evictLocked() bool {
	for i, id := range r.order {
		if s := r.jobs[id].State; s == StateDone || s == StateFailed {
			delete(r.jobs, id)
			r.order = append(r.order[:i], r.order[i+1:]...)
			return true
		}
	}
	return false
}

func (r *registry) get(id string) (JobStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.jobs[id]
	if !ok {
		return JobStatus{}, ErrJobNotFound
	}
	return *st, nil
}

// update applies f to the job under the lock.
func (r *registry) update(id string, f func(*JobStatus)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.jobs[id]; ok {
		f(st)
	}
}
