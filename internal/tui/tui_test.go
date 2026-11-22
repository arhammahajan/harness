package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/arhammahajan/harness/internal/tui/components/chat"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/page"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
)

func newTestAppModel(t *testing.T) *appModel {
	t.Helper()
	m := New(nil)
	am, ok := m.(*appModel)
	if !ok {
		t.Fatalf("expected *appModel, got %T", m)
	}
	return am
}

func TestAppModelViewAndHardwareCursor(t *testing.T) {
	am := newTestAppModel(t)

	// Before sizing, View still returns declarative v2 View fields safely.
	v0 := am.View()
	if !v0.AltScreen {
		t.Fatal("expected AltScreen=true")
	}
	if v0.BackgroundColor == nil {
		t.Fatal("expected non-nil BackgroundColor")
	}
	if v0.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("expected MouseModeCellMotion, got %v", v0.MouseMode)
	}

	// Size the terminal and render.
	_, resizeCmd := am.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if resizeCmd == nil {
		t.Fatal("expected non-nil command on resize (BeginResize)")
	}

	v := am.View()
	if v.Content == "" {
		t.Fatal("expected non-empty rendered content")
	}
	if v.Cursor == nil {
		t.Fatal("expected real hardware cursor from focused editor")
	}
	if v.Cursor.X < 0 || v.Cursor.X >= 120 || v.Cursor.Y < 0 || v.Cursor.Y >= 40 {
		t.Fatalf("cursor out of screen bounds: %+v", v.Cursor)
	}
	if strings.Contains(v.Content, "\r\n") {
		t.Fatal("expected normalized LF newlines, found CRLF")
	}
}

func TestAppModelFrameCacheScrollHitAndModalInvalidation(t *testing.T) {
	am := newTestAppModel(t)
	am.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// First render populates frame cache.
	_ = am.View()
	if am.frames.Len() != 1 {
		t.Fatalf("expected 1 cached frame, got %d", am.frames.Len())
	}
	if am.frames.Hits() != 0 {
		t.Fatalf("expected 0 hits initially, got %d", am.frames.Hits())
	}

	// Scroll-only wheel event (overscroll on empty/bottom chat) should hit cache.
	am.Update(render.CoalescedWheelMsg{DeltaY: 1})
	v1 := am.View()
	if am.frames.Hits() != 1 {
		t.Fatalf("expected frame cache hit on scroll-only overscroll, hits=%d misses=%d", am.frames.Hits(), am.frames.Misses())
	}
	if v1.Content == "" {
		t.Fatal("expected non-empty cached frame content")
	}

	// Opening a modal dialog invalidates the cache and renders the overlay.
	am.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !am.showQuit {
		t.Fatal("expected quit dialog to be open after ctrl+c")
	}
	vQuit := am.View()
	if !strings.Contains(vQuit.Content, "quit") && !strings.Contains(strings.ToLower(vQuit.Content), "yes") {
		t.Fatalf("expected quit dialog content in rendered frame, got:\n%s", vQuit.Content)
	}
	if am.frames.Len() != 0 {
		t.Fatalf("expected frame cache to be reset while modal is open, len=%d", am.frames.Len())
	}

	// Closing quit dialog restores normal chat view and cacheability.
	am.Update(dialog.CloseQuitMsg{})
	if am.showQuit {
		t.Fatal("expected quit dialog closed")
	}
	_ = am.View()
	if am.frames.Len() != 1 {
		t.Fatalf("expected frame cache repopulated after modal closed, len=%d", am.frames.Len())
	}
}

func TestAppModelBackgroundColorAndThemeSwitch(t *testing.T) {
	origDark := theme.HasDarkBackground()
	origTheme := theme.CurrentThemeName()
	t.Cleanup(func() {
		theme.SetHasDarkBackground(origDark)
		_ = theme.SetTheme(origTheme)
	})

	am := newTestAppModel(t)
	am.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	_ = am.View()

	// Light terminal background detection updates theme polarity and invalidates frames.
	am.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}})
	if theme.HasDarkBackground() {
		t.Fatal("expected light background after light BackgroundColorMsg")
	}
	vLight := am.View()
	if vLight.Content == "" {
		t.Fatal("expected non-empty light view")
	}

	// Dark terminal background restores dark polarity.
	am.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 0, G: 0, B: 0, A: 255}})
	if !theme.HasDarkBackground() {
		t.Fatal("expected dark background after dark BackgroundColorMsg")
	}

	// Theme switch restyles chat and invalidates frame cache cleanly.
	am.Update(dialog.ThemeChangedMsg{ThemeName: "gruvbox"})
	vTheme := am.View()
	if vTheme.Content == "" {
		t.Fatal("expected non-empty view after theme change")
	}
}

func TestAppModelOverlaysAndLogsPageDraw(t *testing.T) {
	am := newTestAppModel(t)
	am.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// Help overlay
	am.Update(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
	if !am.showHelp {
		t.Fatal("expected help overlay open")
	}
	if v := am.View(); v.Content == "" {
		t.Fatal("expected non-empty help overlay render")
	}
	am.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if am.showHelp {
		t.Fatal("expected help overlay closed on esc")
	}

	// Multi-arguments overlay with hardware cursor
	am.Update(dialog.ShowMultiArgumentsDialogMsg{
		CommandID: "test",
		Content:   "echo $FOO",
		ArgNames:  []string{"FOO"},
	})
	vArgs := am.View()
	if vArgs.Cursor == nil {
		t.Fatal("expected hardware cursor from MultiArgumentsDialogCmp")
	}
	am.Update(dialog.CloseMultiArgumentsDialogMsg{Submit: false})

	// Compacting overlay
	am.isCompacting = true
	am.compactingMessage = "Working..."
	vCompact := am.View()
	if !strings.Contains(vCompact.Content, "Summarizing") {
		t.Fatal("expected Summarizing overlay on screen")
	}
	am.isCompacting = false

	// Switch to LogsPage and draw via UV layout
	am.Update(page.PageChangeMsg{ID: page.LogsPage})
	if am.currentPage != page.LogsPage {
		t.Fatal("expected currentPage == LogsPage")
	}
	vLogs := am.View()
	if vLogs.Content == "" {
		t.Fatal("expected non-empty LogsPage render")
	}

	// WarmMsg handled cleanly even on LogsPage / ChatPage
	am.Update(page.PageChangeMsg{ID: page.ChatPage})
	am.Update(chat.WarmMsg{Seq: 1})
	am.Update(render.AnimTickMsg{Gen: 999})
	am.Update(render.FrameGCMsg{})
	if v := am.View(); v.Content == "" {
		t.Fatal("expected non-empty ChatPage render after pipeline msgs")
	}
}
