// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"sync"
	"time"
)

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

type Kind string

const (
	KindRecompute  Kind = "pricing_recompute"
	KindVacuum     Kind = "vacuum"
	KindBackup     Kind = "backup"
	KindUserDelete Kind = "user_delete"
)

type JobStatus struct {
	ID         string
	Kind       Kind
	State      State
	Rows       int
	Error      string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
}

type registry struct {
	mu    sync.Mutex
	max   int
	jobs  map[string]*JobStatus
	order []string
}

func newRegistry(maxJobs int) *registry {
	return &registry{max: maxJobs, jobs: make(map[string]*JobStatus)}
}

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

func (r *registry) update(id string, f func(*JobStatus)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st, ok := r.jobs[id]; ok {
		f(st)
	}
}
