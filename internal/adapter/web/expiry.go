// SPDX-License-Identifier: Apache-2.0

package web

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/thnhdatle/loupe/internal/platform/clock"
)

const maxEntries = 1024

type expiringSet struct {
	mu  sync.Mutex
	clk clock.Clock
	m   map[[sha256.Size]byte]time.Time
}

func newExpiringSet(clk clock.Clock) *expiringSet {
	return &expiringSet{clk: clk, m: make(map[[sha256.Size]byte]time.Time)}
}

func (s *expiringSet) add(secret string, ttl time.Duration) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	if len(s.m) >= maxEntries {
		s.evictLocked(now)
	}
	exp := now.Add(ttl)
	s.m[sha256.Sum256([]byte(secret))] = exp
	return exp
}

func (s *expiringSet) evictLocked(now time.Time) {
	var oldest [sha256.Size]byte
	var oldestAt time.Time
	first := true
	for k, exp := range s.m {
		if !now.Before(exp) {
			delete(s.m, k)
			continue
		}
		if first || exp.Before(oldestAt) {
			oldest, oldestAt, first = k, exp, false
		}
	}
	if len(s.m) >= maxEntries {
		delete(s.m, oldest)
	}
}

func (s *expiringSet) has(secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveLocked(sha256.Sum256([]byte(secret)))
}

func (s *expiringSet) take(secret string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := sha256.Sum256([]byte(secret))
	ok := s.liveLocked(k)
	delete(s.m, k)
	return ok
}

func (s *expiringSet) remove(secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, sha256.Sum256([]byte(secret)))
}

func (s *expiringSet) liveLocked(k [sha256.Size]byte) bool {
	exp, ok := s.m[k]
	if !ok {
		return false
	}
	if !s.clk.Now().Before(exp) {
		delete(s.m, k)
		return false
	}
	return true
}
