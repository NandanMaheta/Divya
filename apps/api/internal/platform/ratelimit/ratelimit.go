// Package ratelimit is a small in-memory, per-instance fixed-window limiter (plan P-17). It
// protects the instance from floods; the database-backed throttles remain the shared defence.
package ratelimit

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key    string
	start  time.Time
	count  int
	parent *list.Element
}

// Limiter admits at most limit requests per key in each window. A key's window opens at its first
// request and lasts exactly window; rejected requests do not extend it. The table holds at most
// maxKeys keys: when full, expired keys go first, then the least recently opened one.
type Limiter struct {
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
	byOpen  *list.List // front = oldest window start
}

// New builds a limiter. now is the clock (use time.Now outside tests).
func New(limit int, window time.Duration, maxKeys int, now func() time.Time) *Limiter {
	return &Limiter{
		limit:   limit,
		window:  window,
		maxKeys: max(maxKeys, 1),
		now:     now,
		entries: make(map[string]*entry),
		byOpen:  list.New(),
	}
}

// Allow reports whether the request is admitted (true) or must be answered 429 (false).
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if e, ok := l.entries[key]; ok {
		if now.Before(e.start.Add(l.window)) {
			if e.count >= l.limit {
				return false
			}
			e.count++
			return true
		}
		// Window ended: open a fresh one and move to the back of the opening order.
		e.start, e.count = now, 1
		l.byOpen.MoveToBack(e.parent)
		return l.limit >= 1
	}

	l.makeRoom()
	e := &entry{key: key, start: now, count: 1}
	e.parent = l.byOpen.PushBack(e)
	l.entries[key] = e
	return l.limit >= 1
}

// Len is the number of keys currently tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// makeRoom frees one slot when the table is full. Caller holds mu.
func (l *Limiter) makeRoom() {
	for len(l.entries) >= l.maxKeys {
		front := l.byOpen.Front()
		if front == nil {
			return
		}
		old := front.Value.(*entry)
		// The list is ordered by opening time, so expired keys sit at the front and are evicted first;
		// when every key is live the oldest-opened one is the victim.
		l.byOpen.Remove(front)
		delete(l.entries, old.key)
	}
}
