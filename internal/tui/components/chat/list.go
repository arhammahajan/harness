package chat

import (
	"context"
	"fmt"
	"image"
	"math"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/llm/agent"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/list"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

const (
	// resizeSettleDuration is how long after the last resize event the chat
	// waits before warming off-screen message items.
	resizeSettleDuration = 120 * time.Millisecond

	// warmBatchSize is the number of items prewarmed per frame after resize.
	warmBatchSize = 25

	// scrollbarHideDuration is how long the scrollbar remains visible after
	// scroll activity.
	scrollbarHideDuration = 2 * time.Second

	// scrollbarCols is always reserved on the right edge of the transcript so
	// item wrap widths never shift when the transcript crosses overflow.
	scrollbarCols = 1
)

// RenderState captures all render-affecting state of the messages component so
// frame-level caches can detect changes without rendering.
type RenderState struct {
	OffsetIdx        int
	OffsetLine       int
	ScrollbarVisible bool
	ItemsVersion     uint64
	Working          bool
	MessageCount     int
	SpinnerFrame     int
}

// WarmMsg drives one incremental post-resize prewarm batch.
type WarmMsg struct {
	Seq int
}

// ScrollbarHideMsg hides the auto-hiding scrollbar when its sequence matches.
type ScrollbarHideMsg struct {
	Seq int
}

// MessagesCmp is the chat transcript component interface.
type MessagesCmp interface {
	tea.Model
	layout.Sizeable
	layout.Bindings
	layout.Drawable

	RenderState() RenderState
	BeginResize() tea.Cmd
	WarmStep(seq int) (tea.Cmd, bool)
	EnsureScrollbarHide() tea.Cmd
	Restyle()
	IsAnimating() bool
	AdvanceAnimation() bool
	HandleWheel(msg render.CoalescedWheelMsg) bool
	Follow() bool
	ScrollToBottom()
}

type messagesCmp struct {
	app           *app.App
	width, height int
	session       session.Session
	messages      []message.Message
	currentMsgID  string

	list      *list.List
	drawCache *render.DrawCache

	// Stable item maps keyed by ID so streaming updates mutate items in-place,
	// preserving StreamingMarkdown prefixes and list cache entries.
	userItems      map[string]*UserMessageItem
	assistantItems map[string]*AssistantMessageItem
	toolItems      map[string]*ToolMessageItem
	currentItems   []list.Item

	follow bool

	// Auto-hiding scrollbar state.
	scrollbarVisible   bool
	scrollbarHideSeq   int
	scrollbarHideArmed int

	// Resize settle + incremental prewarm state.
	resizing        bool
	resizeSettleSeq int
	warmNext        int

	// Working spinner frame driven by external AnimClock via AdvanceAnimation.
	spinnerFrame int
}

var _ MessagesCmp = (*messagesCmp)(nil)

type MessageKeys struct {
	PageDown     key.Binding
	PageUp       key.Binding
	HalfPageUp   key.Binding
	HalfPageDown key.Binding
}

var messageKeys = MessageKeys{
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("f/pgdn", "page down"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("b/pgup", "page up"),
	),
	HalfPageUp: key.NewBinding(
		key.WithKeys("ctrl+u"),
		key.WithHelp("ctrl+u", "½ page up"),
	),
	HalfPageDown: key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "½ page down"),
	),
}

func (m *messagesCmp) Init() tea.Cmd {
	return nil
}

func (m *messagesCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dialog.ThemeChangedMsg:
		m.Restyle()
		return m, nil

	case WarmMsg:
		cmd, _ := m.WarmStep(msg.Seq)
		return m, cmd

	case ScrollbarHideMsg:
		m.HideScrollbar(msg.Seq)
		return m, nil

	case SessionSelectedMsg:
		if msg.ID != m.session.ID {
			cmd := m.SetSession(msg)
			return m, cmd
		}
		return m, nil

	case SessionClearedMsg:
		m.session = session.Session{}
		m.messages = nil
		m.currentMsgID = ""
		m.userItems = make(map[string]*UserMessageItem)
		m.assistantItems = make(map[string]*AssistantMessageItem)
		m.toolItems = make(map[string]*ToolMessageItem)
		m.currentItems = nil
		m.list.SetItems()
		m.drawCache.Invalidate()
		m.resetScrollbar()
		m.follow = true
		m.resizing = false
		m.spinnerFrame = 0
		return m, nil

	case render.CoalescedWheelMsg:
		if m.HandleWheel(msg) {
			return m, m.EnsureScrollbarHide()
		}
		return m, nil

	case tea.KeyPressMsg:
		step := max(1, m.listViewportHeight())
		switch {
		case key.Matches(msg, messageKeys.PageUp):
			m.scrollBy(-step)
			return m, m.EnsureScrollbarHide()
		case key.Matches(msg, messageKeys.PageDown):
			m.scrollBy(step)
			return m, m.EnsureScrollbarHide()
		case key.Matches(msg, messageKeys.HalfPageUp):
			m.scrollBy(-max(1, step/2))
			return m, m.EnsureScrollbarHide()
		case key.Matches(msg, messageKeys.HalfPageDown):
			m.scrollBy(max(1, step/2))
			return m, m.EnsureScrollbarHide()
		}

	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.UpdatedEvent && msg.Payload.ID == m.session.ID {
			prevSummary := m.session.SummaryMessageID
			m.session = msg.Payload
			if m.session.SummaryMessageID != prevSummary {
				m.syncItems()
			}
		}

	case pubsub.Event[message.Message]:
		needsSync := false
		if msg.Type == pubsub.CreatedEvent {
			if msg.Payload.SessionID == m.session.ID {
				messageExists := false
				for _, v := range m.messages {
					if v.ID == msg.Payload.ID {
						messageExists = true
						break
					}
				}
				if !messageExists {
					m.messages = append(m.messages, msg.Payload)
					m.currentMsgID = msg.Payload.ID
					needsSync = true
				}
			}
			// Tool calls from a child Task sub-agent session.
			if m.hasToolCallID(msg.Payload.SessionID) {
				needsSync = true
			}
		} else if msg.Type == pubsub.UpdatedEvent {
			if msg.Payload.SessionID == m.session.ID {
				for i, v := range m.messages {
					if v.ID == msg.Payload.ID {
						m.messages[i] = msg.Payload
						needsSync = true
						break
					}
				}
			} else if m.hasToolCallID(msg.Payload.SessionID) {
				needsSync = true
			}
		}
		if needsSync {
			m.syncItems()
		}
	}

	return m, nil
}

func (m *messagesCmp) hasToolCallID(id string) bool {
	if id == "" {
		return false
	}
	for _, v := range m.messages {
		for _, c := range v.ToolCalls() {
			if c.ID == id {
				return true
			}
		}
	}
	return false
}

func (m *messagesCmp) IsAgentWorking() bool {
	if m.app == nil || m.app.CoderAgent == nil {
		return false
	}
	return m.app.CoderAgent.IsSessionBusy(m.session.ID)
}

// IsAnimating reports whether the working indicator spinner needs animation
// clock ticks.
func (m *messagesCmp) IsAnimating() bool {
	return m.IsAgentWorking() && len(m.messages) > 0
}

// AdvanceAnimation advances the working spinner by one frame when animating.
func (m *messagesCmp) AdvanceAnimation() bool {
	if !m.IsAnimating() {
		return false
	}
	frames := spinner.Pulse.Frames
	if len(frames) == 0 {
		return false
	}
	m.spinnerFrame = (m.spinnerFrame + 1) % len(frames)
	return true
}

// RenderState returns the current render-affecting state without rendering.
func (m *messagesCmp) RenderState() RenderState {
	offsetIdx, offsetLine := m.list.ScrollPosition()
	return RenderState{
		OffsetIdx:        offsetIdx,
		OffsetLine:       offsetLine,
		ScrollbarVisible: m.scrollbarVisible,
		ItemsVersion:     m.list.ItemsVersion(),
		Working:          m.IsAgentWorking(),
		MessageCount:     len(m.messages),
		SpinnerFrame:     m.spinnerFrame,
	}
}

// BeginResize suppresses the O(N) total-height scan and scrollbar during live
// resize drags and schedules incremental cache warming once resize settles.
func (m *messagesCmp) BeginResize() tea.Cmd {
	m.resizing = true
	m.resizeSettleSeq++
	m.warmNext = 0
	seq := m.resizeSettleSeq
	return tea.Tick(resizeSettleDuration, func(time.Time) tea.Msg {
		return WarmMsg{Seq: seq}
	})
}

// WarmStep warms the next batch of items into the list cache. Returns a command
// to continue warming (nil when done or stale) and whether warming finished.
func (m *messagesCmp) WarmStep(seq int) (tea.Cmd, bool) {
	if seq != m.resizeSettleSeq {
		return nil, false
	}
	m.warmNext = m.list.Prewarm(m.warmNext, warmBatchSize)
	if m.warmNext >= m.list.Len() {
		m.resizing = false
		return nil, true
	}
	return func() tea.Msg {
		return WarmMsg{Seq: seq}
	}, false
}

// Restyle clears cached rendered output on all items while preserving cached
// heights in the list, and invalidates the screen draw cache.
func (m *messagesCmp) Restyle() {
	m.list.Restyle(func(item list.Item) {
		if msgItem, ok := item.(MessageItem); ok {
			msgItem.ClearCache()
		}
	})
	m.drawCache.Invalidate()
}

// Follow reports whether the transcript is pinned to the bottom.
func (m *messagesCmp) Follow() bool {
	return m.follow
}

// ScrollToBottom pins the transcript to the bottom and enables follow mode.
func (m *messagesCmp) ScrollToBottom() {
	m.list.ScrollToBottom()
	m.follow = true
}

// HandleWheel scrolls the transcript in response to coalesced mouse wheel
// deltas (3 lines per wheel step, minimum ±3 when non-zero).
func (m *messagesCmp) HandleWheel(msg render.CoalescedWheelMsg) bool {
	if msg.DeltaY == 0 || m.list.Len() == 0 {
		return false
	}
	steps := int(math.Round(msg.DeltaY))
	if steps == 0 {
		if msg.DeltaY > 0 {
			steps = 1
		} else {
			steps = -1
		}
	}
	m.scrollBy(steps * 3)
	return true
}

func (m *messagesCmp) scrollBy(lines int) {
	if lines == 0 || m.list.Len() == 0 {
		return
	}
	if m.follow {
		m.list.ScrollToBottom()
	}
	m.list.ScrollBy(lines)
	if lines < 0 {
		m.follow = false
	} else if m.list.AtBottom() {
		m.follow = true
	}
	m.showScrollbar()
}

func (m *messagesCmp) showScrollbar() {
	m.scrollbarVisible = true
	m.scrollbarHideSeq++
}

// EnsureScrollbarHide arms the auto-hide timer when the scrollbar is visible
// and no timer is armed for the current activity sequence.
func (m *messagesCmp) EnsureScrollbarHide() tea.Cmd {
	if !m.scrollbarVisible {
		return nil
	}
	if m.scrollbarHideArmed == m.scrollbarHideSeq {
		return nil
	}
	m.scrollbarHideArmed = m.scrollbarHideSeq
	seq := m.scrollbarHideSeq
	return tea.Tick(scrollbarHideDuration, func(time.Time) tea.Msg {
		return ScrollbarHideMsg{Seq: seq}
	})
}

// HideScrollbar hides the scrollbar when seq matches the latest scroll activity.
func (m *messagesCmp) HideScrollbar(seq int) {
	if seq == m.scrollbarHideSeq {
		m.scrollbarVisible = false
		m.scrollbarHideArmed = 0
	}
}

func (m *messagesCmp) resetScrollbar() {
	m.scrollbarVisible = false
	m.scrollbarHideArmed = 0
}

func (m *messagesCmp) syncItems() {
	toolResults := make(map[string]message.ToolResult)
	for _, msg := range m.messages {
		for _, tr := range msg.ToolResults() {
			if tr.ToolCallID != "" {
				toolResults[tr.ToolCallID] = tr
			}
		}
	}

	items := make([]list.Item, 0, len(m.messages))
	seenUsers := make(map[string]struct{}, len(m.userItems))
	seenAssistants := make(map[string]struct{}, len(m.assistantItems))
	seenTools := make(map[string]struct{}, len(m.toolItems))

	for _, msg := range m.messages {
		switch msg.Role {
		case message.User:
			item, ok := m.userItems[msg.ID]
			if !ok {
				item = NewUserMessageItem(msg)
				m.userItems[msg.ID] = item
			} else {
				item.SetMessage(msg)
			}
			seenUsers[msg.ID] = struct{}{}
			items = append(items, item)

		case message.Assistant:
			isSummary := m.session.SummaryMessageID != "" && m.session.SummaryMessageID == msg.ID
			if shouldRenderAssistantMessage(msg) {
				item, ok := m.assistantItems[msg.ID]
				if !ok {
					item = NewAssistantMessageItem(msg, isSummary)
					m.assistantItems[msg.ID] = item
				} else {
					item.SetMessage(msg, isSummary)
				}
				seenAssistants[msg.ID] = struct{}{}
				items = append(items, item)
			}

			for _, tc := range msg.ToolCalls() {
				var res *message.ToolResult
				if tr, ok := toolResults[tc.ID]; ok {
					r := tr
					res = &r
				}

				var nested []message.ToolCall
				if tc.Name == agent.AgentToolName && m.app != nil && m.app.Messages != nil {
					if taskMessages, err := m.app.Messages.List(context.Background(), tc.ID); err == nil {
						for _, tm := range taskMessages {
							nested = append(nested, tm.ToolCalls()...)
						}
					}
				}

				tItem, ok := m.toolItems[tc.ID]
				if !ok {
					tItem = NewToolMessageItem(tc, res, nested)
					m.toolItems[tc.ID] = tItem
				} else {
					tItem.SetToolCall(tc, res, nested)
				}
				seenTools[tc.ID] = struct{}{}
				items = append(items, tItem)
			}
		}
	}

	for id := range m.userItems {
		if _, ok := seenUsers[id]; !ok {
			delete(m.userItems, id)
		}
	}
	for id := range m.assistantItems {
		if _, ok := seenAssistants[id]; !ok {
			delete(m.assistantItems, id)
		}
	}
	for id := range m.toolItems {
		if _, ok := seenTools[id]; !ok {
			delete(m.toolItems, id)
		}
	}

	// Preserve list scroll offset and cache entries whenever item pointers are
	// unchanged or purely appended (the hot streaming path). Re-anchoring to
	// bottom in follow mode happens lazily in Draw() / scrollBy() so bulk
	// message ingestion never renders off-screen items.
	if itemsEqual(m.currentItems, items) {
		return
	}
	if len(items) > len(m.currentItems) && itemsEqual(m.currentItems, items[:len(m.currentItems)]) {
		m.list.AppendItems(items[len(m.currentItems):]...)
		m.currentItems = items
		return
	}

	m.currentItems = items
	m.list.SetItems(items...)
}

func itemsEqual(a, b []list.Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Draw renders the chat transcript, optional working line, scrollbar, and help
// footer directly onto scr within area.
func (m *messagesCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	w := area.Dx()
	h := area.Dy()

	helpHeight := 0
	if h >= 1 {
		helpHeight = 1
	}

	workingText := m.working(w)
	workingHeight := 0
	if workingText != "" && h >= 2 {
		workingHeight = 1
	}

	transcriptHeight := max(0, h-helpHeight-workingHeight)
	transcriptArea := image.Rect(area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+transcriptHeight)

	if transcriptHeight > 0 {
		if len(m.messages) == 0 {
			m.drawCache.Draw(scr, transcriptArea, m.initialScreen(w))
		} else {
			listW := max(0, w-scrollbarCols)
			listArea := image.Rect(transcriptArea.Min.X, transcriptArea.Min.Y, transcriptArea.Min.X+listW, transcriptArea.Max.Y)
			if m.list.Width() != listW || m.list.Height() != transcriptHeight {
				m.list.SetSize(listW, transcriptHeight)
			}

			if m.follow {
				m.list.ScrollToBottom()
			}
			rendered := m.list.Render()
			m.drawCache.Draw(scr, listArea, rendered)

			scrollbarArea := image.Rect(transcriptArea.Max.X-scrollbarCols, transcriptArea.Min.Y, transcriptArea.Max.X, transcriptArea.Max.Y)
			layout.ClearArea(scr, scrollbarArea)
			if !m.resizing && m.scrollbarVisible && m.list.Overflows(m.list.Height()) {
				t := theme.CurrentTheme()
				thumbStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Background(t.Background())
				trackStyle := lipgloss.NewStyle().Foreground(t.BorderDim()).Background(t.Background())
				sb := layout.Scrollbar(transcriptHeight, m.list.TotalHeight(), transcriptHeight, m.list.Offset(), thumbStyle, trackStyle)
				if sb != "" {
					layout.DrawAt(scr, scrollbarArea, sb)
				}
			}
		}
	}

	if workingHeight > 0 {
		workingArea := image.Rect(area.Min.X, transcriptArea.Max.Y, area.Max.X, transcriptArea.Max.Y+workingHeight)
		layout.DrawAt(scr, workingArea, workingText)
	}

	if helpHeight > 0 {
		helpArea := image.Rect(area.Min.X, area.Max.Y-helpHeight, area.Max.X, area.Max.Y)
		layout.DrawAt(scr, helpArea, m.help(w))
	}

	return nil
}

// View renders the component into a standalone tea.View via a temporary screen
// buffer so View and Draw produce identical output.
func (m *messagesCmp) View() tea.View {
	w := max(1, m.width)
	h := max(1, m.height)
	buf := uv.NewScreenBuffer(w, h)
	cur := m.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func hasToolsWithoutResponse(messages []message.Message) bool {
	toolCalls := make([]message.ToolCall, 0)
	toolResults := make([]message.ToolResult, 0)
	for _, m := range messages {
		toolCalls = append(toolCalls, m.ToolCalls()...)
		toolResults = append(toolResults, m.ToolResults()...)
	}

	for _, v := range toolCalls {
		found := false
		for _, r := range toolResults {
			if v.ID == r.ToolCallID {
				found = true
				break
			}
		}
		if !found && v.Finished {
			return true
		}
	}
	return false
}

func hasUnfinishedToolCalls(messages []message.Message) bool {
	for _, m := range messages {
		for _, v := range m.ToolCalls() {
			if !v.Finished {
				return true
			}
		}
	}
	return false
}

func (m *messagesCmp) working(width int) string {
	if !m.IsAgentWorking() || len(m.messages) == 0 {
		return ""
	}
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	task := "Thinking..."
	lastMessage := m.messages[len(m.messages)-1]
	if hasToolsWithoutResponse(m.messages) {
		task = "Waiting for tool response..."
	} else if hasUnfinishedToolCalls(m.messages) {
		task = "Building tool call..."
	} else if !lastMessage.IsFinished() {
		task = "Generating..."
	}

	frame := ""
	if frames := spinner.Pulse.Frames; len(frames) > 0 {
		frame = frames[m.spinnerFrame%len(frames)]
	}
	return baseStyle.
		Width(max(1, width)).
		Foreground(t.Primary()).
		Bold(true).
		Render(fmt.Sprintf("%s %s ", frame, task))
}

func (m *messagesCmp) help(width int) string {
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	isBusy := m.app != nil && m.app.CoderAgent != nil && m.app.CoderAgent.IsBusy()
	var text string
	if isBusy {
		text = lipgloss.JoinHorizontal(
			lipgloss.Top,
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render("press "),
			baseStyle.Foreground(t.Text()).Bold(true).Render("esc"),
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render(" to exit cancel"),
		)
	} else {
		text = lipgloss.JoinHorizontal(
			lipgloss.Top,
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render("press "),
			baseStyle.Foreground(t.Text()).Bold(true).Render("enter"),
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render(" to send the message,"),
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render(" write"),
			baseStyle.Foreground(t.Text()).Bold(true).Render(" \\"),
			baseStyle.Foreground(t.TextMuted()).Bold(true).Render(" and enter to add a new line"),
		)
	}
	return baseStyle.
		Width(max(1, width)).
		Render(text)
}

func (m *messagesCmp) initialScreen(width int) string {
	width = max(1, width)
	baseStyle := styles.BaseStyle()

	return baseStyle.Width(width).Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			header(width),
			"",
			lspsConfigured(width),
		),
	)
}

func (m *messagesCmp) listViewportHeight() int {
	if h := m.list.Height(); h > 0 {
		return h
	}
	workingH := 0
	if m.IsAgentWorking() && len(m.messages) > 0 {
		workingH = 1
	}
	return max(0, m.height-workingH-1)
}

func (m *messagesCmp) syncListSize() {
	workingH := 0
	if m.IsAgentWorking() && len(m.messages) > 0 {
		workingH = 1
	}
	listW := max(0, m.width-scrollbarCols)
	listH := max(0, m.height-workingH-1)
	m.list.SetSize(listW, listH)
}

func (m *messagesCmp) SetSize(width, height int) tea.Cmd {
	if m.width == width && m.height == height {
		return nil
	}
	wasFollowing := m.follow || m.list.AtBottom()
	m.width = width
	m.height = height
	m.syncListSize()
	if wasFollowing {
		m.ScrollToBottom()
	}
	return nil
}

func (m *messagesCmp) GetSize() (int, int) {
	return m.width, m.height
}

func (m *messagesCmp) SetSession(sess session.Session) tea.Cmd {
	if m.session.ID == sess.ID {
		return nil
	}
	m.session = sess
	m.userItems = make(map[string]*UserMessageItem)
	m.assistantItems = make(map[string]*AssistantMessageItem)
	m.toolItems = make(map[string]*ToolMessageItem)
	m.currentItems = nil
	m.resetScrollbar()
	m.follow = true

	if m.app != nil && m.app.Messages != nil {
		messages, err := m.app.Messages.List(context.Background(), sess.ID)
		if err != nil {
			return util.ReportError(err)
		}
		m.messages = messages
	} else {
		m.messages = nil
	}

	if len(m.messages) > 0 {
		m.currentMsgID = m.messages[len(m.messages)-1].ID
	} else {
		m.currentMsgID = ""
	}
	m.syncItems()
	m.ScrollToBottom()
	return nil
}

func (m *messagesCmp) BindingKeys() []key.Binding {
	return []key.Binding{
		messageKeys.PageDown,
		messageKeys.PageUp,
		messageKeys.HalfPageUp,
		messageKeys.HalfPageDown,
	}
}

func NewMessagesCmp(app *app.App) tea.Model {
	l := list.NewList()
	l.SetGap(1)
	return &messagesCmp{
		app:            app,
		list:           l,
		drawCache:      render.NewDrawCache(),
		userItems:      make(map[string]*UserMessageItem),
		assistantItems: make(map[string]*AssistantMessageItem),
		toolItems:      make(map[string]*ToolMessageItem),
		follow:         true,
	}
}
