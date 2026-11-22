package render

import (
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterPassThrough(t *testing.T) {
	f := NewFilter()

	keyMsg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	assert.Equal(t, keyMsg, f.Filter(nil, keyMsg))

	winMsg := tea.WindowSizeMsg{Width: 80, Height: 24}
	assert.Equal(t, winMsg, f.Filter(nil, winMsg))
}

func TestFilterWheelCoalescingAndReversal(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := NewFilter()
	f.SetNow(func() time.Time { return now })

	wheelDown := tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelDown})
	wheelUp := tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelUp})
	wheelRight := tea.MouseWheelMsg(tea.Mouse{X: 10, Y: 5, Button: tea.MouseWheelRight})

	// First wheel event passes immediately with delta 1.
	got := f.Filter(nil, wheelDown)
	cw, ok := got.(CoalescedWheelMsg)
	require.True(t, ok, "expected CoalescedWheelMsg")
	assert.Equal(t, float64(0), cw.DeltaX)
	assert.Equal(t, float64(1), cw.DeltaY)
	assert.Equal(t, 10, cw.Mouse.X)
	assert.Equal(t, 5, cw.Mouse.Y)

	// Events within the 16ms window are buffered (return nil).
	now = now.Add(5 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelDown))
	now = now.Add(5 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelDown))

	// Once 16ms have elapsed, the accumulated delta is emitted.
	now = now.Add(10 * time.Millisecond)
	got = f.Filter(nil, wheelDown)
	cw, ok = got.(CoalescedWheelMsg)
	require.True(t, ok)
	assert.Equal(t, float64(3), cw.DeltaY)

	// Direction reversal within the window resets pending opposite-sign delta.
	now = now.Add(4 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelDown))
	now = now.Add(4 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelDown))
	now = now.Add(4 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelUp)) // reverses: drops +2, sets -1
	now = now.Add(10 * time.Millisecond)
	got = f.Filter(nil, wheelUp)
	cw, ok = got.(CoalescedWheelMsg)
	require.True(t, ok)
	assert.Equal(t, float64(-2), cw.DeltaY)

	// Horizontal wheel events aggregate DeltaX.
	now = now.Add(4 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, wheelRight))
	now = now.Add(20 * time.Millisecond)
	got = f.Filter(nil, wheelRight)
	cw, ok = got.(CoalescedWheelMsg)
	require.True(t, ok)
	assert.Equal(t, float64(2), cw.DeltaX)
	assert.Equal(t, float64(0), cw.DeltaY)
}

func TestFilterMotionThrottlingIndependentOfWheel(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := NewFilter()
	f.SetNow(func() time.Time { return now })

	motion1 := tea.MouseMotionMsg(tea.Mouse{X: 1, Y: 1})
	motion2 := tea.MouseMotionMsg(tea.Mouse{X: 2, Y: 2})
	motion3 := tea.MouseMotionMsg(tea.Mouse{X: 3, Y: 3})
	wheel := tea.MouseWheelMsg(tea.Mouse{X: 1, Y: 1, Button: tea.MouseWheelDown})

	// First motion passes.
	assert.Equal(t, motion1, f.Filter(nil, motion1))

	// Wheel at the same timestamp still passes (independent stream).
	_, ok := f.Filter(nil, wheel).(CoalescedWheelMsg)
	assert.True(t, ok)

	// Motion within 16ms is dropped.
	now = now.Add(10 * time.Millisecond)
	assert.Nil(t, f.Filter(nil, motion2))

	// Motion at or after 16ms passes.
	now = now.Add(6 * time.Millisecond)
	assert.Equal(t, motion3, f.Filter(nil, motion3))
}

func TestFrameCacheHitMissTTLLRUReset(t *testing.T) {
	type key struct {
		w, h, offset int
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFrameCache[key](3*time.Second, 2)
	fc.SetNow(func() time.Time { return now })

	// Miss on empty cache.
	_, _, ok := fc.Get(key{80, 24, 0})
	assert.False(t, ok)
	assert.Equal(t, 0, fc.Hits())
	assert.Equal(t, 1, fc.Misses())

	// Put with cursor and verify defensive copy.
	inputCur := tea.NewCursor(5, 3)
	fc.Put(key{80, 24, 0}, "frame-0", inputCur)
	inputCur.X = 99 // mutate caller's cursor

	content, cur, ok := fc.Get(key{80, 24, 0})
	require.True(t, ok)
	assert.Equal(t, "frame-0", content)
	require.NotNil(t, cur)
	assert.Equal(t, 5, cur.X)
	assert.Equal(t, 3, cur.Y)
	cur.Y = 77 // mutate returned cursor

	_, cur2, ok := fc.Get(key{80, 24, 0})
	require.True(t, ok)
	assert.Equal(t, 3, cur2.Y)
	assert.Equal(t, 2, fc.Hits())

	// Add second entry, touch first so second becomes LRU.
	now = now.Add(100 * time.Millisecond)
	fc.Put(key{80, 24, 1}, "frame-1", nil)
	now = now.Add(100 * time.Millisecond)
	_, _, ok = fc.Get(key{80, 24, 0}) // refreshes lastUsed on key 0
	require.True(t, ok)

	// Adding third entry evicts LRU (key 1), preserving hot key 0.
	now = now.Add(100 * time.Millisecond)
	fc.Put(key{80, 24, 2}, "frame-2", nil)
	assert.Equal(t, 2, fc.Len())

	_, _, ok = fc.Get(key{80, 24, 1})
	assert.False(t, ok, "LRU entry should have been evicted")
	_, _, ok = fc.Get(key{80, 24, 0})
	assert.True(t, ok, "hot entry should remain")

	// TTL expires based on render time (at), not lastUsed.
	now = now.Add(2800 * time.Millisecond) // total 3100ms since key 0 was rendered, 2800ms since key 2
	_, _, ok = fc.Get(key{80, 24, 0})
	assert.False(t, ok, "key 0 should expire past TTL")
	_, _, ok = fc.Get(key{80, 24, 2})
	assert.True(t, ok, "key 2 should still be valid")

	// GC sweeps expired entries.
	fc.GC(now.Add(5 * time.Second))
	assert.Equal(t, 0, fc.Len())

	// Reset drops all entries.
	fc.Put(key{80, 24, 10}, "frame-10", nil)
	assert.Equal(t, 1, fc.Len())
	fc.Reset()
	assert.Equal(t, 0, fc.Len())
}

func TestAnimClockGenerationFreezeAndRecovery(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var clk AnimClock
	clk.SetNow(func() time.Time { return now })
	clk.SetInterval(1 * time.Millisecond)

	// Not wanted -> nil command.
	assert.Nil(t, clk.Ensure(func() bool { return false }))

	// Wanted -> arms generation 1.
	cmd := clk.Ensure(func() bool { return true })
	require.NotNil(t, cmd)

	// Already running -> does not consult wanted or re-arm.
	wantedCalled := false
	assert.Nil(t, clk.Ensure(func() bool {
		wantedCalled = true
		return true
	}))
	assert.False(t, wantedCalled)

	// Run command and verify generation 1 tick is accepted.
	msg := cmd()
	tick1, ok := msg.(AnimTickMsg)
	require.True(t, ok)
	assert.Equal(t, uint64(1), tick1.Gen)

	// Stale generation is rejected.
	assert.False(t, clk.Accept(AnimTickMsg{Gen: 0}))
	assert.False(t, clk.Accept(AnimTickMsg{Gen: 99}))

	// Matching generation is accepted and stops the clock.
	assert.True(t, clk.Accept(tick1))

	// Next Ensure arms generation 2.
	cmd2 := clk.Ensure(func() bool { return true })
	require.NotNil(t, cmd2)
	tick2 := cmd2().(AnimTickMsg)
	assert.Equal(t, uint64(2), tick2.Gen)

	// Freezing suppresses Ensure and rejects the in-flight tick2.
	clk.SetFrozen(true)
	assert.True(t, clk.Frozen())
	assert.Nil(t, clk.Ensure(func() bool { return true }))
	assert.False(t, clk.Accept(tick2))

	// Unfreezing allows arming a fresh generation (> 2).
	clk.SetFrozen(false)
	assert.False(t, clk.Frozen())
	cmd3 := clk.Ensure(func() bool { return true })
	require.NotNil(t, cmd3)
	tick3 := cmd3().(AnimTickMsg)
	assert.Greater(t, tick3.Gen, tick2.Gen)

	// Lost-tick recovery: if tick3 never arrives and clock advances >= AnimClockLostAfter,
	// Ensure arms a replacement generation and tick3 is rejected if it arrives late.
	now = now.Add(AnimClockLostAfter)
	cmd4 := clk.Ensure(func() bool { return true })
	require.NotNil(t, cmd4)
	tick4 := cmd4().(AnimTickMsg)
	assert.Equal(t, tick3.Gen+1, tick4.Gen)
	assert.False(t, clk.Accept(tick3), "late tick from lost generation must be rejected")
	assert.True(t, clk.Accept(tick4))
}

func TestDrawCacheHitMissMethodChangeAndBlitting(t *testing.T) {
	dc := NewDrawCache()
	scr := uv.NewScreenBuffer(20, 4)
	scr.Method = ansi.GraphemeWidth
	area := image.Rect(0, 0, 20, 4)

	styled := "\x1b[32mhello\x1b[0m\nworld"

	// First draw misses and populates cache.
	dc.Draw(scr, area, styled)
	assert.Equal(t, 0, dc.Hits())
	assert.Equal(t, 1, dc.Misses())
	assert.Equal(t, "h", scr.CellAt(0, 0).Content)
	assert.Equal(t, "w", scr.CellAt(0, 1).Content)

	// Second identical draw hits and blits to a different offset area.
	scr2 := uv.NewScreenBuffer(20, 6)
	scr2.Method = ansi.GraphemeWidth
	dc.Draw(scr2, image.Rect(3, 2, 20, 6), styled)
	assert.Equal(t, 1, dc.Hits())
	assert.Equal(t, 1, dc.Misses())
	assert.Equal(t, "h", scr2.CellAt(3, 2).Content)
	assert.Equal(t, "o", scr2.CellAt(7, 2).Content)
	assert.Equal(t, "w", scr2.CellAt(3, 3).Content)

	// Shorter content misses and clears leftover cells from previous frame.
	dc.Draw(scr, area, "hi")
	assert.Equal(t, 1, dc.Hits())
	assert.Equal(t, 2, dc.Misses())
	assert.Equal(t, "h", scr.CellAt(0, 0).Content)
	assert.Equal(t, "i", scr.CellAt(1, 0).Content)
	assert.Equal(t, " ", scr.CellAt(2, 0).Content, "leftover cells must be cleared")
	assert.Equal(t, " ", scr.CellAt(0, 1).Content, "leftover lines must be cleared")

	// Switching width method forces a miss.
	scr.Method = ansi.WcWidth
	dc.Draw(scr, area, "hi")
	assert.Equal(t, 1, dc.Hits())
	assert.Equal(t, 3, dc.Misses())

	// Same string + same method hits again.
	dc.Draw(scr, area, "hi")
	assert.Equal(t, 2, dc.Hits())
	assert.Equal(t, 3, dc.Misses())

	// Invalidate forces next draw to miss.
	dc.Invalidate()
	dc.Draw(scr, area, "hi")
	assert.Equal(t, 2, dc.Hits())
	assert.Equal(t, 4, dc.Misses())
}

func TestCapabilitiesUpdatesAndQueryCmd(t *testing.T) {
	var caps Capabilities

	assert.False(t, caps.SupportsTrueColor())
	cw, ch := caps.CellSize()
	assert.Equal(t, 0, cw)
	assert.Equal(t, 0, ch)

	caps.Update(tea.EnvMsg{"TERM=xterm-kitty", "COLORTERM=truecolor"})
	assert.Equal(t, "xterm-kitty", caps.Env.Getenv("TERM"))

	caps.Update(tea.ColorProfileMsg{Profile: colorprofile.TrueColor})
	assert.True(t, caps.SupportsTrueColor())

	caps.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	caps.Update(uv.PixelSizeEvent{Width: 1000, Height: 800})
	cw, ch = caps.CellSize()
	assert.Equal(t, 10, cw)
	assert.Equal(t, 20, ch)

	caps.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 15, G: 15, B: 20, A: 255}})
	assert.True(t, caps.DarkBackground)
	caps.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 250, G: 250, B: 250, A: 255}})
	assert.False(t, caps.DarkBackground)

	caps.Update(uv.KittyGraphicsEvent{})
	assert.True(t, caps.SupportsKittyGraphics())

	caps.Update(uv.PrimaryDeviceAttributesEvent{62, 1, 2})
	assert.False(t, caps.SupportsSixelGraphics())
	caps.Update(uv.PrimaryDeviceAttributesEvent{62, 4, 22})
	assert.True(t, caps.SupportsSixelGraphics())

	caps.Update(tea.TerminalVersionMsg{Name: "kitty 0.36.0"})
	assert.Equal(t, "kitty 0.36.0", caps.TerminalVersion)

	caps.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeNotRecognized})
	assert.False(t, caps.ReportFocusEvents)
	caps.Update(tea.ModeReportMsg{Mode: ansi.ModeFocusEvent, Value: ansi.ModeSet})
	assert.True(t, caps.ReportFocusEvents)

	// QueryCmd builds sequences appropriate to environment.
	normalCmd := QueryCmd(uv.Environ{"TERM=xterm-256color"})
	require.NotNil(t, normalCmd)
	normalRaw, ok := normalCmd().(tea.RawMsg).Msg.(string)
	require.True(t, ok)
	assert.Contains(t, normalRaw, ansi.RequestPrimaryDeviceAttributes)
	assert.Contains(t, normalRaw, ansi.RequestNameVersion)

	tmuxCmd := QueryCmd(uv.Environ{"TERM=tmux-256color", "TMUX=/tmp/tmux-1000/default,1,0"})
	require.NotNil(t, tmuxCmd)
	tmuxRaw, ok := tmuxCmd().(tea.RawMsg).Msg.(string)
	require.True(t, ok)
	assert.True(t, strings.Contains(tmuxRaw, "\x1bPtmux;"), "expected tmux passthrough for kitty query")

	appleCmd := QueryCmd(uv.Environ{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"})
	require.NotNil(t, appleCmd)
	appleRaw, ok := appleCmd().(tea.RawMsg).Msg.(string)
	require.True(t, ok)
	assert.NotContains(t, appleRaw, ansi.RequestNameVersion, "Apple_Terminal should skip XTVERSION")
}
