package render

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// DefaultFrameCacheTTL bounds how long a memoized frame may be served after
	// it was rendered. It is a safety valve: even if a state change slips past
	// invalidation, a stale frame is replaced within one TTL of the next update.
	DefaultFrameCacheTTL = 3 * time.Second

	// DefaultFrameCacheMaxEntries caps the number of memoized frames to bound
	// worst-case memory usage.
	DefaultFrameCacheMaxEntries = 32
)

// FrameGCMsg is sent on a timer to sweep expired frames when the UI is
// otherwise idle, so a burst of scrolling does not pin memory indefinitely.
type FrameGCMsg struct{}

type frameEntry struct {
	content string
	cursor  *tea.Cursor
	// at is the render time and drives TTL expiry; lastUsed is the last hit
	// and drives eviction, so a hot frame (e.g. the clamped bottom during
	// overscroll) is kept while still expiring on schedule.
	at       time.Time
	lastUsed time.Time
}

// FrameCache memoizes fully rendered frames keyed by caller-defined state K.
//
// Bubble Tea calls View after every Update, so a flood of wheel events pays
// for a full redraw per event even when the scroll offset did not move or moved
// back to a position rendered a moment ago. Serving those frames from memory
// keeps the event loop draining instead of blocking input behind redraw work.
type FrameCache[K comparable] struct {
	entries map[K]*frameEntry
	ttl     time.Duration
	max     int
	now     func() time.Time

	hits   int
	misses int
}

// NewFrameCache returns a FrameCache with the given TTL and maximum entry
// count. Non-positive values fall back to DefaultFrameCacheTTL and
// DefaultFrameCacheMaxEntries respectively.
func NewFrameCache[K comparable](ttl time.Duration, maxEntries int) *FrameCache[K] {
	if ttl <= 0 {
		ttl = DefaultFrameCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultFrameCacheMaxEntries
	}
	return &FrameCache[K]{
		entries: make(map[K]*frameEntry, maxEntries),
		ttl:     ttl,
		max:     maxEntries,
		now:     time.Now,
	}
}

// SetNow overrides the clock function used for TTL checks and LRU timestamps.
// Passing nil restores time.Now. Primarily intended for deterministic tests.
func (c *FrameCache[K]) SetNow(now func() time.Time) {
	if now == nil {
		c.now = time.Now
		return
	}
	c.now = now
}

// TTL returns the configured time-to-live for cached frames.
func (c *FrameCache[K]) TTL() time.Duration {
	return c.ttl
}

// Len returns the number of memoized frames, including expired ones that have
// not yet been swept.
func (c *FrameCache[K]) Len() int {
	return len(c.entries)
}

// Hits returns the cumulative number of cache hits.
func (c *FrameCache[K]) Hits() int {
	return c.hits
}

// Misses returns the cumulative number of cache misses (including expired
// lookups).
func (c *FrameCache[K]) Misses() int {
	return c.misses
}

func (c *FrameCache[K]) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *FrameCache[K]) expired(e *frameEntry, now time.Time) bool {
	return now.Sub(e.at) > c.ttl
}

// Get returns the memoized frame for key if present and within TTL.
// The returned cursor (if non-nil) is a copy so callers cannot mutate cached
// state.
func (c *FrameCache[K]) Get(key K) (string, *tea.Cursor, bool) {
	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return "", nil, false
	}
	now := c.clock()
	if c.expired(e, now) {
		delete(c.entries, key)
		c.misses++
		return "", nil, false
	}
	c.hits++
	e.lastUsed = now
	if e.cursor == nil {
		return e.content, nil, true
	}
	cur := *e.cursor
	return e.content, &cur, true
}

// Put memoizes a frame, sweeping expired entries first and evicting the least
// recently used entry when the cache is full.
func (c *FrameCache[K]) Put(key K, content string, cursor *tea.Cursor) {
	now := c.clock()
	c.GC(now)
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.max {
		c.evictLRU()
	}
	var cur *tea.Cursor
	if cursor != nil {
		copied := *cursor
		cur = &copied
	}
	c.entries[key] = &frameEntry{
		content:  content,
		cursor:   cur,
		at:       now,
		lastUsed: now,
	}
}

// GC removes every entry older than the TTL relative to now.
func (c *FrameCache[K]) GC(now time.Time) {
	for key, e := range c.entries {
		if c.expired(e, now) {
			delete(c.entries, key)
		}
	}
}

func (c *FrameCache[K]) evictLRU() {
	var (
		victimKey K
		victim    *frameEntry
	)
	for key, e := range c.entries {
		if victim == nil || e.lastUsed.Before(victim.lastUsed) {
			victimKey, victim = key, e
		}
	}
	if victim != nil {
		delete(c.entries, victimKey)
	}
}

// Reset drops every memoized frame.
func (c *FrameCache[K]) Reset() {
	if len(c.entries) == 0 {
		return
	}
	clear(c.entries)
}
