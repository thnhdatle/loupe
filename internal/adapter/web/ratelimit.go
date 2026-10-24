// SPDX-License-Identifier: Apache-2.0

package web

import (
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const (
	loginFailLimit  = 5
	loginFailWindow = time.Minute
	maxLimiterIPs   = 10_000
)

type attempts struct {
	start time.Time
	fails int
}

type loginLimiter struct {
	mu  sync.Mutex
	clk clock.Clock
	m   map[string]*attempts
}

func newLoginLimiter(clk clock.Clock) *loginLimiter {
	return &loginLimiter{clk: clk, m: make(map[string]*attempts)}
}

func (l *loginLimiter) blocked(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[ip]
	if a == nil {
		return 0, false
	}
	left := a.start.Add(loginFailWindow).Sub(l.clk.Now())
	if left <= 0 {
		delete(l.m, ip)
		return 0, false
	}
	return left, a.fails >= loginFailLimit
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	a := l.m[ip]
	if a == nil || !now.Before(a.start.Add(loginFailWindow)) {
		if a == nil && len(l.m) >= maxLimiterIPs {
			l.evictLocked(now)
		}
		a = &attempts{start: now}
		l.m[ip] = a
	}
	a.fails++
}

func (l *loginLimiter) evictLocked(now time.Time) {
	for ip, a := range l.m {
		if !now.Before(a.start.Add(loginFailWindow)) {
			delete(l.m, ip)
		}
	}
	for ip := range l.m {
		if len(l.m) < maxLimiterIPs {
			return
		}
		delete(l.m, ip)
	}
}
