package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/config"
	"github.com/arhammahajan/harness/internal/llm/agent"
	"github.com/arhammahajan/harness/internal/llm/models"
	"github.com/arhammahajan/harness/internal/llm/tools"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
)

type stubAgentService struct {
	busy bool
}

func (s *stubAgentService) Subscribe(context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	return nil
}
func (s *stubAgentService) Model() models.Model { return models.Model{} }
func (s *stubAgentService) Run(context.Context, string, string, ...message.Attachment) (<-chan agent.AgentEvent, error) {
	return nil, nil
}
func (s *stubAgentService) Cancel(string)                    {}
func (s *stubAgentService) IsSessionBusy(string) bool        { return s.busy }
func (s *stubAgentService) IsBusy() bool                     { return s.busy }
func (s *stubAgentService) Update(config.AgentName, models.ModelID) (models.Model, error) {
	return models.Model{}, nil
}
func (s *stubAgentService) Summarize(context.Context, string) error { return nil }

func makeUserMsg(id, sessID, text string) message.Message {
	return message.Message{
		ID:        id,
		Role:      message.User,
		SessionID: sessID,
		Parts:     []message.ContentPart{message.TextContent{Text: text}},
	}
}

func makeAssistantMsg(id, sessID, thinking, text string, finished bool) message.Message {
	var parts []message.ContentPart
	if thinking != "" {
		parts = append(parts, message.ReasoningContent{Thinking: thinking})
	}
	if text != "" {
		parts = append(parts, message.TextContent{Text: text})
	}
	if finished {
		parts = append(parts, message.Finish{Reason: message.FinishReasonEndTurn, Time: 2500})
	}
	return message.Message{
		ID:        id,
		Role:      message.Assistant,
		SessionID: sessID,
		Model:     models.Claude37Sonnet,
		CreatedAt: 1000,
		Parts:     parts,
	}
}

func TestAssistantMessageItemStreamingAndSectionCaches(t *testing.T) {
	thinking := "First thought paragraph.\n\nSecond thought paragraph.\n\nConcluding thought."
	msg := makeAssistantMsg("a1", "s1", thinking, "", false)

	item := NewAssistantMessageItem(msg, false)
	v0 := item.Version()

	// Initial render: thinking section rendered, misses once.
	out1 := ansi.Strip(item.Render(80))
	if !strings.Contains(out1, "First thought paragraph.") {
		t.Fatalf("expected thinking content in initial render, got:\n%s", out1)
	}
	if item.thinkingSec.misses != 1 || item.thinkingSec.hits != 0 {
		t.Fatalf("expected thinkingSec misses=1 hits=0, got misses=%d hits=%d", item.thinkingSec.misses, item.thinkingSec.hits)
	}

	// Stream body content paragraph 1 + 2.
	msg.AppendContent("First answer paragraph.\n\nSecond answer paragraph.")
	item.SetMessage(msg, false)
	if item.Version() != v0+1 {
		t.Fatalf("expected Version bump on content change: got %d, want %d", item.Version(), v0+1)
	}
	out2 := ansi.Strip(item.Render(80))
	if !strings.Contains(out2, "First answer paragraph.") {
		t.Fatalf("expected body content in render, got:\n%s", out2)
	}
	if item.thinkingSec.misses != 1 || item.thinkingSec.hits != 1 {
		t.Fatalf("expected thinking cache HIT while content streams: misses=%d hits=%d", item.thinkingSec.misses, item.thinkingSec.hits)
	}

	// Stream another paragraph so StreamingMarkdown promotes a stable prefix.
	msg.AppendContent("\n\nThird streaming paragraph")
	item.SetMessage(msg, false)
	_ = item.Render(80)
	if item.thinkingSec.misses != 1 || item.thinkingSec.hits != 2 {
		t.Fatalf("expected thinking cache to stay hot: misses=%d hits=%d", item.thinkingSec.misses, item.thinkingSec.hits)
	}
	prefixLen := item.streamingContent.StablePrefixLen()
	if prefixLen == 0 {
		t.Fatalf("expected StreamingMarkdown stable prefix > 0 after multi-paragraph streaming")
	}

	// Stream trailing tokens in the last paragraph: stable prefix stays intact, thinking still hits.
	msg.AppendContent(" with more tokens!")
	item.SetMessage(msg, false)
	out3 := ansi.Strip(item.Render(80))
	if !strings.Contains(out3, "with more tokens!") {
		t.Fatalf("expected latest streaming delta in output, got:\n%s", out3)
	}
	if item.thinkingSec.misses != 1 || item.thinkingSec.hits != 3 {
		t.Fatalf("expected thinking cache HIT=3 misses=1, got misses=%d hits=%d", item.thinkingSec.misses, item.thinkingSec.hits)
	}
	if got := item.streamingContent.StablePrefixLen(); got < prefixLen {
		t.Fatalf("expected stable prefix to be preserved (>= %d), got %d", prefixLen, got)
	}

	// Calling SetMessage with identical message must be a no-op (no version bump).
	vStable := item.Version()
	item.SetMessage(msg, false)
	if item.Version() != vStable {
		t.Fatalf("expected no version bump on identical SetMessage, got %d vs %d", item.Version(), vStable)
	}

	// Finish with summary flag and verify footer + (summary) suffix.
	msg.Parts = append(msg.Parts, message.Finish{Reason: message.FinishReasonEndTurn, Time: 2500})
	item.SetMessage(msg, true)
	outSummary := ansi.Strip(item.Render(80))
	if !strings.Contains(outSummary, "(summary)") {
		t.Fatalf("expected (summary) suffix in output, got:\n%s", outSummary)
	}

	// ClearCache resets all section and streaming caches and bumps version.
	vBeforeClear := item.Version()
	item.ClearCache()
	if item.Version() != vBeforeClear+1 {
		t.Fatalf("expected ClearCache to bump version")
	}
	if item.streamingContent.StablePrefixLen() != 0 {
		t.Fatalf("expected ClearCache to reset streamingContent")
	}
	if item.thinkingSec.valid || item.contentSec.valid {
		t.Fatalf("expected ClearCache to invalidate section caches")
	}

	// Finished-without-output fallback.
	emptyFinished := makeAssistantMsg("a2", "s1", "", "", true)
	emptyItem := NewAssistantMessageItem(emptyFinished, false)
	if got := ansi.Strip(emptyItem.Render(80)); !strings.Contains(got, "Finished without output") {
		t.Fatalf("expected finished-without-output fallback, got:\n%s", got)
	}
}

func TestToolMessageItemCachingAndRenderers(t *testing.T) {
	bashInput, _ := json.Marshal(tools.BashParams{Command: "echo hi"})
	tc := message.ToolCall{
		ID:       "tc1",
		Name:     tools.BashToolName,
		Input:    string(bashInput),
		Finished: false,
	}

	item := NewToolMessageItem(tc, nil, nil)
	v0 := item.Version()

	outUnfinished := item.Render(80)
	if !strings.Contains(outUnfinished, "Bash:") || !strings.Contains(outUnfinished, "Building command...") {
		t.Fatalf("expected unfinished Bash progress text, got:\n%s", outUnfinished)
	}

	// Identical SetToolCall does not bump version.
	item.SetToolCall(tc, nil, nil)
	if item.Version() != v0 {
		t.Fatalf("expected no version bump on identical SetToolCall")
	}

	// Finish tool call without response yet.
	tc.Finished = true
	item.SetToolCall(tc, nil, nil)
	if item.Version() != v0+1 {
		t.Fatalf("expected version bump when tool finishes")
	}
	if out := item.Render(80); !strings.Contains(out, "Waiting for response...") {
		t.Fatalf("expected waiting message, got:\n%s", out)
	}

	// Provide result.
	res := &message.ToolResult{ToolCallID: "tc1", Name: tools.BashToolName, Content: "hi"}
	item.SetToolCall(tc, res, nil)
	if out := item.Render(80); !strings.Contains(out, "hi") {
		t.Fatalf("expected tool response output, got:\n%s", out)
	}

	// Error result formatting.
	errRes := &message.ToolResult{ToolCallID: "tc1", Name: tools.BashToolName, Content: "boom", IsError: true}
	item.SetToolCall(tc, errRes, nil)
	if out := item.Render(80); !strings.Contains(out, "Error: boom") {
		t.Fatalf("expected error formatting, got:\n%s", out)
	}

	// Task tool with nested tool calls.
	taskInput, _ := json.Marshal(agent.AgentParams{Prompt: "inspect repo"})
	globInput, _ := json.Marshal(tools.GlobParams{Pattern: "*.go"})
	taskTC := message.ToolCall{ID: "task1", Name: agent.AgentToolName, Input: string(taskInput), Finished: true}
	nested := []message.ToolCall{{ID: "g1", Name: tools.GlobToolName, Input: string(globInput), Finished: true}}
	taskRes := &message.ToolResult{ToolCallID: "task1", Name: agent.AgentToolName, Content: "Done."}
	taskItem := NewToolMessageItem(taskTC, taskRes, nested)
	taskOut := taskItem.Render(80)
	if !strings.Contains(taskOut, "Task:") || !strings.Contains(taskOut, "Glob:") || !strings.Contains(taskOut, "Done.") {
		t.Fatalf("expected Task + nested Glob + result, got:\n%s", taskOut)
	}
}

func TestMessagesCmpLazyVirtualizationAndDrawCache(t *testing.T) {
	m := NewMessagesCmp(nil).(*messagesCmp)
	m.session = session.Session{ID: "sess-1", Title: "Perf Test"}
	m.SetSize(100, 30)

	// Add 200 messages (100 user + 100 assistant).
	for i := range 200 {
		var msg message.Message
		if i%2 == 0 {
			msg = makeUserMsg(fmt.Sprintf("u-%d", i), "sess-1", fmt.Sprintf("User message number %d", i))
		} else {
			msg = makeAssistantMsg(fmt.Sprintf("a-%d", i), "sess-1", "", fmt.Sprintf("Assistant reply %d\n\nSecond paragraph %d.", i, i), true)
		}
		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: msg,
		})
	}

	if m.list.Len() != 200 {
		t.Fatalf("expected 200 list items, got %d", m.list.Len())
	}

	scr := uv.NewScreenBuffer(100, 30)
	m.Draw(scr, scr.Bounds())

	// Verify lazy virtualization: item 0 (far off-screen above the 30-row viewport)
	// was never rendered.
	firstUser := m.userItems["u-0"]
	if firstUser == nil {
		t.Fatalf("expected u-0 in userItems")
	}
	if firstUser.cachedRender != "" {
		t.Fatalf("expected off-screen item u-0 to remain unrendered (lazy virtualization)")
	}

	// Verify DrawCache: 1 miss on first draw, 1 hit on identical second draw.
	if m.drawCache.Misses() != 1 || m.drawCache.Hits() != 0 {
		t.Fatalf("expected DrawCache misses=1 hits=0 after first draw, got misses=%d hits=%d", m.drawCache.Misses(), m.drawCache.Hits())
	}
	m.Draw(scr, scr.Bounds())
	if m.drawCache.Misses() != 1 || m.drawCache.Hits() != 1 {
		t.Fatalf("expected DrawCache hit on identical second frame, got misses=%d hits=%d", m.drawCache.Misses(), m.drawCache.Hits())
	}

	// Verify streaming update mutates the last AssistantMessageItem IN-PLACE and
	// preserves its StreamingMarkdown stable prefix.
	lastItem := m.assistantItems["a-199"]
	if lastItem == nil {
		t.Fatalf("expected a-199 in assistantItems")
	}
	updated := m.messages[199]
	updated.AppendContent("\n\nStreaming delta paragraph")
	m.Update(pubsub.Event[message.Message]{
		Type:    pubsub.UpdatedEvent,
		Payload: updated,
	})
	if m.assistantItems["a-199"] != lastItem {
		t.Fatalf("expected streaming update to mutate AssistantMessageItem in-place")
	}
	m.Draw(scr, scr.Bounds())
	if lastItem.streamingContent.StablePrefixLen() == 0 {
		t.Fatalf("expected in-place streaming update to preserve/build StreamingMarkdown stable prefix")
	}
}

func TestMessagesCmpBeginResizeWarmStepAndScrollbarSuppression(t *testing.T) {
	m := NewMessagesCmp(nil).(*messagesCmp)
	m.session = session.Session{ID: "sess-resize"}
	m.SetSize(100, 20)

	for i := range 60 {
		msg := makeUserMsg(fmt.Sprintf("u-%d", i), "sess-resize", fmt.Sprintf("Line %d content for overflow", i))
		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: msg,
		})
	}

	// Scroll up so scrollbar becomes visible.
	if !m.HandleWheel(render.CoalescedWheelMsg{DeltaY: -3}) {
		t.Fatalf("expected HandleWheel to return true")
	}
	if !m.scrollbarVisible {
		t.Fatalf("expected scrollbarVisible=true after wheel scroll")
	}

	// BeginResize suppresses scrollbar and returns settle tick command.
	cmd := m.BeginResize()
	if cmd == nil {
		t.Fatalf("expected BeginResize to return a settle tea.Cmd")
	}
	if !m.resizing {
		t.Fatalf("expected resizing=true after BeginResize")
	}

	scr := uv.NewScreenBuffer(100, 20)
	m.Draw(scr, scr.Bounds())

	// Check rightmost column of transcript (rows 0..18) has no scrollbar glyph while resizing.
	hasScrollbarGlyph := func(buf uv.ScreenBuffer) bool {
		for y := 0; y < 19; y++ {
			cell := buf.CellAt(99, y)
			if cell != nil && (cell.Content == layout.ScrollbarThumb || cell.Content == layout.ScrollbarTrack) {
				return true
			}
		}
		return false
	}
	if hasScrollbarGlyph(scr) {
		t.Fatalf("expected scrollbar to be suppressed during resize")
	}

	seq := m.resizeSettleSeq

	// Stale seq is a no-op.
	if staleCmd, staleDone := m.WarmStep(seq - 1); staleCmd != nil || staleDone {
		t.Fatalf("expected stale WarmStep to return (nil, false), got (%v, %v)", staleCmd, staleDone)
	}

	// Batch 1: 0 -> 25
	nextCmd, done := m.WarmStep(seq)
	if done || nextCmd == nil || m.warmNext != 25 {
		t.Fatalf("batch 1: expected done=false nextCmd!=nil warmNext=25, got done=%v cmd=%v warmNext=%d", done, nextCmd != nil, m.warmNext)
	}

	// Batch 2: 25 -> 50
	nextCmd, done = m.WarmStep(seq)
	if done || nextCmd == nil || m.warmNext != 50 {
		t.Fatalf("batch 2: expected done=false nextCmd!=nil warmNext=50, got done=%v cmd=%v warmNext=%d", done, nextCmd != nil, m.warmNext)
	}

	// Batch 3: 50 -> 60 (done)
	nextCmd, done = m.WarmStep(seq)
	if !done || nextCmd != nil || m.resizing {
		t.Fatalf("batch 3: expected done=true nextCmd=nil resizing=false, got done=%v cmd=%v resizing=%v", done, nextCmd != nil, m.resizing)
	}

	// Redraw: scrollbar should now appear in rightmost column.
	m.Draw(scr, scr.Bounds())
	if !hasScrollbarGlyph(scr) {
		t.Fatalf("expected scrollbar glyphs after warming completed")
	}

	// EnsureScrollbarHide arms once and dedupes until activity bumps seq.
	hideCmd1 := m.EnsureScrollbarHide()
	if hideCmd1 == nil {
		t.Fatalf("expected EnsureScrollbarHide to arm timer")
	}
	if hideCmd2 := m.EnsureScrollbarHide(); hideCmd2 != nil {
		t.Fatalf("expected EnsureScrollbarHide to dedupe already-armed timer")
	}

	// Stale ScrollbarHideMsg must not hide scrollbar; matching seq must hide it.
	m.Update(ScrollbarHideMsg{Seq: m.scrollbarHideSeq - 1})
	if !m.scrollbarVisible {
		t.Fatalf("expected stale ScrollbarHideMsg to be ignored")
	}
	m.Update(ScrollbarHideMsg{Seq: m.scrollbarHideSeq})
	if m.scrollbarVisible {
		t.Fatalf("expected matching ScrollbarHideMsg to hide scrollbar")
	}
}

func TestMessagesCmpWheelScrollFollowRenderStateAndAnimation(t *testing.T) {
	stubAgent := &stubAgentService{busy: true}
	m := NewMessagesCmp(&app.App{CoderAgent: stubAgent}).(*messagesCmp)
	m.session = session.Session{ID: "sess-scroll"}
	m.SetSize(100, 20)

	// Init must NOT return a spinning tick command.
	if cmd := m.Init(); cmd != nil {
		t.Fatalf("expected Init() to return nil (no independent spinner tick)")
	}

	for i := range 30 {
		msg := makeUserMsg(fmt.Sprintf("u-%d", i), "sess-scroll", fmt.Sprintf("Message %d", i))
		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: msg,
		})
	}

	scr := uv.NewScreenBuffer(100, 20)
	m.Draw(scr, scr.Bounds())

	if !m.Follow() {
		t.Fatalf("expected Follow() to start true")
	}
	rs1 := m.RenderState()

	// Scroll up via CoalescedWheelMsg disables follow and changes RenderState.
	if !m.HandleWheel(render.CoalescedWheelMsg{DeltaY: -2}) {
		t.Fatalf("expected HandleWheel(-2) to return true")
	}
	if m.Follow() {
		t.Fatalf("expected Follow()=false after scrolling up")
	}
	rs2 := m.RenderState()
	if rs2 == rs1 {
		t.Fatalf("expected RenderState to change after scroll: %+v vs %+v", rs1, rs2)
	}

	// Small non-zero fractional delta still scrolls at least 3 lines.
	idxBefore, lineBefore := m.list.ScrollPosition()
	if !m.HandleWheel(render.CoalescedWheelMsg{DeltaY: -0.2}) {
		t.Fatalf("expected HandleWheel(-0.2) to return true")
	}
	idxAfter, lineAfter := m.list.ScrollPosition()
	if idxAfter == idxBefore && lineAfter == lineBefore {
		t.Fatalf("expected small fractional non-zero DeltaY to still scroll")
	}

	// ScrollToBottom re-enables follow.
	m.ScrollToBottom()
	if !m.Follow() {
		t.Fatalf("expected Follow()=true after ScrollToBottom()")
	}

	// Animation hook: IsAnimating & AdvanceAnimation.
	if !m.IsAnimating() {
		t.Fatalf("expected IsAnimating()=true when agent busy and messages non-empty")
	}
	rsAnim1 := m.RenderState()
	if !m.AdvanceAnimation() {
		t.Fatalf("expected AdvanceAnimation()=true while animating")
	}
	rsAnim2 := m.RenderState()
	if rsAnim2.SpinnerFrame != (rsAnim1.SpinnerFrame+1)%len(spinner.Pulse.Frames) {
		t.Fatalf("expected SpinnerFrame to advance, got %d -> %d", rsAnim1.SpinnerFrame, rsAnim2.SpinnerFrame)
	}

	stubAgent.busy = false
	if m.IsAnimating() {
		t.Fatalf("expected IsAnimating()=false when agent idle")
	}
	if m.AdvanceAnimation() {
		t.Fatalf("expected AdvanceAnimation()=false when agent idle")
	}
}

func TestMessagesCmpRestylePreservesHeights(t *testing.T) {
	m := NewMessagesCmp(nil).(*messagesCmp)
	m.session = session.Session{ID: "sess-restyle"}
	m.SetSize(100, 20)

	for i := range 40 {
		msg := makeUserMsg(fmt.Sprintf("u-%d", i), "sess-restyle", fmt.Sprintf("Restyle message %d", i))
		m.Update(pubsub.Event[message.Message]{
			Type:    pubsub.CreatedEvent,
			Payload: msg,
		})
	}

	// Prewarm all items so every item has a cached height in m.list.
	m.list.Prewarm(0, 40)
	heightBefore := m.list.TotalHeight()
	rsBefore := m.RenderState()

	// Trigger theme change via dialog.ThemeChangedMsg -> Restyle().
	theme.SetHasDarkBackground(!theme.HasDarkBackground())
	defer theme.SetHasDarkBackground(!theme.HasDarkBackground())
	m.Update(dialog.ThemeChangedMsg{ThemeName: "toggled"})

	rsAfter := m.RenderState()
	if rsAfter.ItemsVersion == rsBefore.ItemsVersion {
		t.Fatalf("expected ItemsVersion to change after Restyle()")
	}

	// Verify item 0's cache was cleared.
	if m.userItems["u-0"].cachedRender != "" {
		t.Fatalf("expected Restyle to clear item caches")
	}

	// Verify cached height is preserved without re-rendering off-screen item 0!
	heightAfter := m.list.TotalHeight()
	if heightAfter != heightBefore {
		t.Fatalf("expected TotalHeight to stay %d after Restyle, got %d", heightBefore, heightAfter)
	}
	if m.userItems["u-0"].cachedRender != "" {
		t.Fatalf("expected TotalHeight after Restyle to reuse cached height without re-rendering off-screen u-0")
	}

	// Drawing the viewport re-renders only visible items (e.g. u-39).
	scr := uv.NewScreenBuffer(100, 20)
	m.Draw(scr, scr.Bounds())
	if m.userItems["u-39"].cachedRender == "" {
		t.Fatalf("expected visible item u-39 to be re-rendered on Draw after Restyle")
	}
	if m.userItems["u-0"].cachedRender != "" {
		t.Fatalf("expected off-screen item u-0 to remain unrendered after Draw")
	}
}

func TestEditorCmpDrawAndHardwareCursor(t *testing.T) {
	ed := NewEditorCmp(nil).(*editorCmp)

	// Init must not start synthetic blink ticks; virtual cursor must be disabled.
	if cmd := ed.Init(); cmd != nil {
		t.Fatalf("expected editor Init() to return nil")
	}
	if ed.textarea.VirtualCursor() {
		t.Fatalf("expected textarea VirtualCursor to be false")
	}

	ed.SetSize(80, 5)
	scr := uv.NewScreenBuffer(100, 30)
	area := image.Rect(10, 20, 90, 25)

	cur1 := ed.Draw(scr, area)
	if cur1 == nil {
		t.Fatalf("expected non-nil hardware cursor when editor is focused")
	}
	if cur1.X < area.Min.X+editorPromptWidth || cur1.X >= area.Max.X {
		t.Fatalf("expected cursor X within [%d, %d), got %d", area.Min.X+editorPromptWidth, area.Max.X, cur1.X)
	}
	if cur1.Y != area.Min.Y {
		t.Fatalf("expected cursor Y=%d without attachments, got %d", area.Min.Y, cur1.Y)
	}

	// Type "hello" and verify cursor advances right by 5 cells.
	for _, ch := range "hello" {
		ed.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)})
	}
	cur2 := ed.Draw(scr, area)
	if cur2 == nil {
		t.Fatalf("expected non-nil cursor after typing")
	}
	if cur2.X != cur1.X+5 {
		t.Fatalf("expected cursor X to move from %d to %d, got %d", cur1.X, cur1.X+5, cur2.X)
	}

	// Add attachment and verify cursor shifts down by 1 row.
	ed.Update(dialog.AttachmentAddedMsg{
		Attachment: message.Attachment{FileName: "diagram.png", FilePath: "/tmp/diagram.png", MimeType: "image/png"},
	})
	cur3 := ed.Draw(scr, area)
	if cur3 == nil {
		t.Fatalf("expected non-nil cursor with attachment")
	}
	if cur3.Y != area.Min.Y+1 {
		t.Fatalf("expected cursor Y=%d with attachment row, got %d", area.Min.Y+1, cur3.Y)
	}

	// Delete attachment via ctrl+r + '0'.
	ed.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	ed.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	if len(ed.attachments) != 0 {
		t.Fatalf("expected attachment 0 to be deleted, got %d remaining", len(ed.attachments))
	}

	// Backslash + Enter inserts newline instead of sending.
	ed.textarea.SetValue("line1\\")
	_, cmd := ed.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected backslash+enter not to send message")
	}
	if ed.textarea.Value() != "line1\n" {
		t.Fatalf("expected backslash+enter to append newline, got %q", ed.textarea.Value())
	}

	// View returns tea.View with hardware cursor set.
	v := ed.View()
	if v.Cursor == nil {
		t.Fatalf("expected View().Cursor to be non-nil when focused")
	}
}

func TestSidebarCmpDraw(t *testing.T) {
	sb := NewSidebarCmp(session.Session{ID: "s1", Title: "Refactor Chat"}, nil).(*sidebarCmp)
	sb.SetSize(40, 25)

	scr := uv.NewScreenBuffer(40, 25)
	if cur := sb.Draw(scr, scr.Bounds()); cur != nil {
		t.Fatalf("expected sidebar Draw to return nil cursor")
	}

	rendered := scr.Render()
	for _, want := range []string{"Harness", "Session", "Refactor Chat", "Modified Files:", "No modified files"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected sidebar render to contain %q, got:\n%s", want, rendered)
		}
	}

	// Second identical draw hits DrawCache.
	if sb.drawCache.Misses() != 1 || sb.drawCache.Hits() != 0 {
		t.Fatalf("expected sidebar drawCache misses=1 hits=0, got misses=%d hits=%d", sb.drawCache.Misses(), sb.drawCache.Hits())
	}
	sb.Draw(scr, scr.Bounds())
	if sb.drawCache.Misses() != 1 || sb.drawCache.Hits() != 1 {
		t.Fatalf("expected sidebar drawCache hit on repeat draw, got misses=%d hits=%d", sb.drawCache.Misses(), sb.drawCache.Hits())
	}

	v := sb.View()
	if !strings.Contains(v.Content, "Refactor Chat") {
		t.Fatalf("expected sidebar View() to contain session title")
	}
}
