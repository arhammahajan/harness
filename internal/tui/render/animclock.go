package render

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// DefaultFrameInterval is the default tick period for the shared animation
	// clock (~12.5 fps, matching Crush's spinner cadence).
	DefaultFrameInterval = 80 * time.Millisecond

	// AnimClockLostAfter is how long an armed clock may go without its tick
	// arriving before Ensure assumes the command was lost and arms a new one.
	// Generous enough that a slow frame never trips it.
	AnimClockLostAfter = 2 * time.Second
)

// AnimTickMsg is one frame of the shared animation clock. Gen is the clock
// generation the tick was armed for.
type AnimTickMsg struct {
	Gen uint64
}

// AnimClock drives every spinner and animation in the UI from a single
// generation-guarded timer. There is at most one tick outstanding: a tick
// carrying an older generation belongs to a superseded clock and is ignored, so
// a delayed tick can never run alongside its replacement.
//
// The zero value is a stopped, unfrozen clock using DefaultFrameInterval.
type AnimClock struct {
	// frozen keeps ghost spinners still (e.g. a reloaded idle session).
	frozen bool

	running  bool
	gen      uint64
	armedAt  time.Time
	interval time.Duration
	now      func() time.Time
}

// Ensure arms the clock if no tick is outstanding and wanted reports something
// to animate. wanted is only consulted when a tick could be armed, so the
// per-update cost of an already running clock stays constant.
func (c *AnimClock) Ensure(wanted func() bool) tea.Cmd {
	if c.frozen {
		c.running = false
		return nil
	}
	if c.running && c.clock().Sub(c.armedAt) < AnimClockLostAfter {
		return nil
	}
	if wanted == nil || !wanted() {
		c.running = false
		return nil
	}
	c.running = true
	c.armedAt = c.clock()
	c.gen++
	gen := c.gen
	interval := c.interval
	if interval <= 0 {
		interval = DefaultFrameInterval
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return AnimTickMsg{Gen: gen}
	})
}

// Accept consumes a tick, reporting whether it belongs to the live clock.
// When accepted, the clock transitions to stopped until the next Ensure call.
func (c *AnimClock) Accept(msg AnimTickMsg) bool {
	if c.frozen || msg.Gen != c.gen {
		return false
	}
	c.running = false
	return true
}

// SetFrozen freezes or unfreezes the animation clock. While frozen, Ensure
// returns nil and any in-flight tick from an earlier generation is rejected.
func (c *AnimClock) SetFrozen(frozen bool) {
	c.frozen = frozen
	if frozen {
		c.running = false
		c.gen++
	}
}

// Frozen reports whether the animation clock is currently frozen.
func (c *AnimClock) Frozen() bool {
	return c.frozen
}

// SetInterval configures the tick interval. Non-positive values revert to
// DefaultFrameInterval.
func (c *AnimClock) SetInterval(d time.Duration) {
	c.interval = d
}

// SetNow overrides the clock function used to detect lost ticks. Passing nil
// restores time.Now. Primarily intended for deterministic tests.
func (c *AnimClock) SetNow(now func() time.Time) {
	c.now = now
}

func (c *AnimClock) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
