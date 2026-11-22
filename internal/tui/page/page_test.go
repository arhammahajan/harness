package page

import (
	"fmt"
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/chat"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	_ "github.com/arhammahajan/harness/internal/tui/theme"
)

type stubCompletionProvider struct {
	items []dialog.CompletionItemI
}

func (s *stubCompletionProvider) GetId() string { return "stub" }

func (s *stubCompletionProvider) GetEntry() dialog.CompletionItemI {
	return dialog.NewCompletionItem(dialog.CompletionItem{Title: "Stub", Value: "stub"})
}

func (s *stubCompletionProvider) GetChildEntries(query string) ([]dialog.CompletionItemI, error) {
	if query == "" {
		return s.items, nil
	}
	var out []dialog.CompletionItemI
	for _, it := range s.items {
		if strings.Contains(strings.ToLower(it.GetValue()), strings.ToLower(query)) {
			out = append(out, it)
		}
	}
	return out, nil
}

func TestChatPageSizeDrawAndHardwareCursor(t *testing.T) {
	model := NewChatPage(nil)
	cp, ok := model.(ChatPageModel)
	if !ok {
		t.Fatalf("NewChatPage(nil) does not implement ChatPageModel")
	}

	cp.Init()
	cp.SetSize(120, 40)

	w, h := cp.GetSize()
	if w != 120 || h != 40 {
		t.Fatalf("GetSize() = (%d, %d), want (120, 40)", w, h)
	}

	buf := uv.NewScreenBuffer(120, 40)
	cur := cp.Draw(buf, buf.Bounds())
	if cur == nil {
		t.Fatalf("Draw() returned nil cursor; expected hardware cursor from focused editor")
	}

	// Default split verticalRatio is 0.9: top=36 rows, editor=4 rows [36..39] with top border at row 36.
	if cur.Y < 36 || cur.Y >= 40 {
		t.Errorf("cursor Y = %d, expected within editor band [36, 40)", cur.Y)
	}
	if cur.X < 0 || cur.X >= 120 {
		t.Errorf("cursor X = %d, expected within [0, 120)", cur.X)
	}

	rendered := buf.Render()
	if rendered == "" {
		t.Fatalf("Draw() produced empty screen buffer")
	}
	if !strings.Contains(rendered, ">") {
		t.Errorf("Draw() output missing editor prompt '>'")
	}

	v := cp.View()
	if v.Content == "" {
		t.Errorf("View().Content is empty")
	}
	if v.Cursor == nil {
		t.Errorf("View().Cursor is nil; expected hardware cursor")
	} else if *v.Cursor != *cur {
		t.Errorf("View().Cursor = %+v, want %+v", *v.Cursor, *cur)
	}

	if len(cp.BindingKeys()) == 0 {
		t.Errorf("BindingKeys() returned empty slice")
	}
}

func TestChatPageDrawNonZeroOriginTranslatesCursor(t *testing.T) {
	cp := NewChatPage(nil).(ChatPageModel)
	cp.SetSize(100, 30)

	buf := uv.NewScreenBuffer(120, 40)
	area := image.Rect(5, 3, 105, 33)
	cur := cp.Draw(buf, area)
	if cur == nil {
		t.Fatalf("Draw() returned nil cursor")
	}
	if !image.Pt(cur.X, cur.Y).In(area) {
		t.Errorf("cursor (%d, %d) outside draw area %v", cur.X, cur.Y, area)
	}
	if cur.Y < area.Min.Y+27 {
		t.Errorf("cursor Y = %d, expected in bottom editor strip (>= %d)", cur.Y, area.Min.Y+27)
	}
}

func TestChatPageCompletionPopupDrawAndLifecycle(t *testing.T) {
	raw := NewChatPage(nil).(*chatPage)
	raw.completionDialog = dialog.NewCompletionDialogCmp(&stubCompletionProvider{
		items: []dialog.CompletionItemI{
			dialog.NewCompletionItem(dialog.CompletionItem{Title: "alpha.go", Value: "alpha.go"}),
			dialog.NewCompletionItem(dialog.CompletionItem{Title: "beta.go", Value: "beta.go"}),
		},
	})
	raw.SetSize(120, 40)

	if raw.CompletionOpen() {
		t.Fatalf("CompletionOpen() = true before '@' keypress")
	}

	baseBuf := uv.NewScreenBuffer(120, 40)
	raw.Draw(baseBuf, baseBuf.Bounds())
	baseRender := baseBuf.Render()

	// Trigger completion popup with '@'.
	updated, _ := raw.Update(tea.KeyPressMsg{Code: '@', Text: "@"})
	cp := updated.(ChatPageModel)
	if !cp.CompletionOpen() {
		t.Fatalf("CompletionOpen() = false after '@' keypress")
	}

	popupBuf := uv.NewScreenBuffer(120, 40)
	cur := cp.Draw(popupBuf, popupBuf.Bounds())
	if cur == nil {
		t.Fatalf("Draw() with completion popup returned nil hardware cursor")
	}
	if cur.Y < 36 || cur.Y >= 40 {
		t.Errorf("cursor Y = %d while completion popup open, want inside editor [36, 40)", cur.Y)
	}

	popupRender := popupBuf.Render()
	if popupRender == baseRender {
		t.Fatalf("Draw() output with completion popup open matches closed state")
	}
	if !strings.Contains(popupRender, "alpha.go") {
		t.Errorf("completion popup missing expected item 'alpha.go'")
	}

	// Pressing enter while completion popup is open should be handled by completion dialog (not forwarded to editor send).
	updated, enterCmd := cp.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	cp = updated.(ChatPageModel)
	if enterCmd == nil {
		t.Errorf("expected non-nil cmd when selecting completion item with enter")
	}

	// Closing message clears CompletionOpen().
	updated, _ = cp.Update(dialog.CompletionDialogCloseMsg{})
	cp = updated.(ChatPageModel)
	if cp.CompletionOpen() {
		t.Fatalf("CompletionOpen() = true after CompletionDialogCloseMsg")
	}
}

func TestChatPagePipelineHooks(t *testing.T) {
	cp := NewChatPage(nil).(ChatPageModel)
	cp.SetSize(100, 30)

	state := cp.ChatRenderState()
	if state.MessageCount != 0 || state.Working || state.ScrollbarVisible {
		t.Fatalf("unexpected initial RenderState: %+v", state)
	}

	if cp.IsAnimating() {
		t.Errorf("IsAnimating() = true when idle")
	}
	if cp.AdvanceAnimation() {
		t.Errorf("AdvanceAnimation() = true when idle")
	}
	if cmd := cp.EnsureScrollbarHide(); cmd != nil {
		t.Errorf("EnsureScrollbarHide() = non-nil when scrollbar hidden")
	}

	resizeCmd := cp.BeginResize()
	if resizeCmd == nil {
		t.Fatalf("BeginResize() returned nil cmd")
	}
	// Stale seq returns (nil, false).
	if cmd, done := cp.WarmStep(999); cmd != nil || done {
		t.Errorf("WarmStep(stale) = (%v, %v), want (nil, false)", cmd, done)
	}
	// Matching seq (1) finishes immediately on empty transcript.
	if cmd, done := cp.WarmStep(1); cmd != nil || !done {
		t.Errorf("WarmStep(1) = (%v, %v), want (nil, true)", cmd, done)
	}

	// Restyle should be safe to invoke.
	cp.Restyle()

	// Select a session and inject enough messages to overflow the viewport.
	sess := session.Session{ID: "sess-1", Title: "Test"}
	m, _ := cp.Update(chat.SessionSelectedMsg(sess))
	cp = m.(ChatPageModel)

	for i := range 20 {
		msg := message.Message{
			ID:        fmt.Sprintf("msg-%d", i),
			SessionID: sess.ID,
			Role:      message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: fmt.Sprintf("Message line %d\nSecond line %d\nThird line %d", i, i, i)},
			},
		}
		m, _ = cp.Update(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: msg,
		})
		cp = m.(ChatPageModel)
	}

	// Render once so the list lays out items and anchors to bottom.
	buf := uv.NewScreenBuffer(100, 30)
	cp.Draw(buf, buf.Bounds())

	state = cp.ChatRenderState()
	if state.MessageCount != 20 {
		t.Fatalf("ChatRenderState().MessageCount = %d, want 20", state.MessageCount)
	}

	// Wheel scroll up should be handled and make scrollbar visible.
	if !cp.HandleWheel(render.CoalescedWheelMsg{DeltaY: -2}) {
		t.Fatalf("HandleWheel() returned false on non-empty transcript")
	}
	if !cp.ChatRenderState().ScrollbarVisible {
		t.Errorf("ScrollbarVisible = false after wheel scroll")
	}
	if cmd := cp.EnsureScrollbarHide(); cmd == nil {
		t.Errorf("EnsureScrollbarHide() = nil when scrollbar visible")
	}
	// Second call before activity changes should be a no-op.
	if cmd := cp.EnsureScrollbarHide(); cmd != nil {
		t.Errorf("second EnsureScrollbarHide() = non-nil without new scroll activity")
	}

	// ctrl+n clears session.
	m, clearCmd := cp.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	cp = m.(ChatPageModel)
	if clearCmd == nil {
		t.Fatalf("ctrl+n returned nil cmd")
	}
	m, _ = cp.Update(chat.SessionClearedMsg{})
	cp = m.(ChatPageModel)
	if got := cp.ChatRenderState().MessageCount; got != 0 {
		t.Errorf("MessageCount after SessionClearedMsg = %d, want 0", got)
	}
}

func TestLogsPageSizeAndDraw(t *testing.T) {
	lp := NewLogsPage()

	// Compile-time check that LogPage embeds layout.Drawable.
	var _ layout.Drawable = lp

	lp.Init()
	lp.SetSize(120, 40)

	w, h := lp.GetSize()
	if w != 120 || h != 40 {
		t.Fatalf("GetSize() = (%d, %d), want (120, 40)", w, h)
	}

	buf := uv.NewScreenBuffer(120, 40)
	if cur := lp.Draw(buf, buf.Bounds()); cur != nil {
		t.Errorf("LogsPage.Draw() returned non-nil cursor %+v", cur)
	}

	rendered := buf.Render()
	if rendered == "" {
		t.Fatalf("LogsPage.Draw() produced empty buffer")
	}
	if !strings.Contains(rendered, "┌") || !strings.Contains(rendered, "└") {
		t.Errorf("LogsPage.Draw() missing container borders")
	}

	v := lp.View()
	if v.Content != rendered {
		t.Errorf("LogsPage.View() output differs from Draw()")
	}

	// Verify odd height via WindowSizeMsg splits cleanly without dropping a row.
	updated, _ := lp.Update(tea.WindowSizeMsg{Width: 120, Height: 41})
	lp = updated.(LogPage)
	w, h = lp.GetSize()
	if w != 120 || h != 41 {
		t.Fatalf("after WindowSizeMsg GetSize() = (%d, %d), want (120, 41)", w, h)
	}
	oddBuf := uv.NewScreenBuffer(120, 41)
	lp.Draw(oddBuf, oddBuf.Bounds())
	if oddBuf.Render() == "" {
		t.Fatalf("LogsPage.Draw() with odd height produced empty buffer")
	}

	if len(lp.BindingKeys()) == 0 {
		t.Errorf("LogsPage.BindingKeys() returned empty slice")
	}
}
