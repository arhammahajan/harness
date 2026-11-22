package page

import (
	"context"
	"image"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/completions"
	"github.com/arhammahajan/harness/internal/history"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/chat"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

var ChatPage PageID = "chat"

// ChatPageModel exposes the chat page's Bubble Tea model, layout interfaces,
// and rendering pipeline hooks for the root TUI model.
type ChatPageModel interface {
	tea.Model
	layout.Sizeable
	layout.Bindings
	layout.Drawable

	ChatRenderState() chat.RenderState
	BeginResize() tea.Cmd
	WarmStep(seq int) (tea.Cmd, bool)
	EnsureScrollbarHide() tea.Cmd
	Restyle()
	IsAnimating() bool
	AdvanceAnimation() bool
	HandleWheel(msg render.CoalescedWheelMsg) bool
	CompletionOpen() bool
}

type chatPage struct {
	app                  *app.App
	editor               layout.Container
	messages             layout.Container
	messagesCmp          chat.MessagesCmp
	layout               layout.SplitPaneLayout
	session              session.Session
	completionDialog     dialog.CompletionDialog
	showCompletionDialog bool
}

var _ ChatPageModel = (*chatPage)(nil)

type ChatKeyMap struct {
	ShowCompletionDialog key.Binding
	NewSession           key.Binding
	Cancel               key.Binding
}

var keyMap = ChatKeyMap{
	ShowCompletionDialog: key.NewBinding(
		key.WithKeys("@"),
		key.WithHelp("@", "Complete"),
	),
	NewSession: key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "new session"),
	),
	Cancel: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel"),
	),
}

func themeBackground() color.Color {
	if t := theme.CurrentTheme(); t != nil {
		return t.Background()
	}
	return nil
}

func themeBorderNormal() color.Color {
	if t := theme.CurrentTheme(); t != nil {
		return t.BorderNormal()
	}
	return nil
}

func (p *chatPage) Init() tea.Cmd {
	cmds := []tea.Cmd{
		p.layout.Init(),
		p.completionDialog.Init(),
	}
	return tea.Batch(cmds...)
}

func (p *chatPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		cmd := p.layout.SetSize(msg.Width, msg.Height)
		cmds = append(cmds, cmd)
	case dialog.CompletionDialogCloseMsg:
		p.showCompletionDialog = false
	case chat.SendMsg:
		cmd := p.sendMessage(msg.Text, msg.Attachments)
		if cmd != nil {
			return p, cmd
		}
	case dialog.CommandRunCustomMsg:
		// Check if the agent is busy before executing custom commands
		if p.app != nil && p.app.CoderAgent != nil && p.app.CoderAgent.IsBusy() {
			return p, util.ReportWarn("Agent is busy, please wait before executing a command...")
		}

		// Process the command content with arguments if any
		content := msg.Content
		if msg.Args != nil {
			// Replace all named arguments with their values
			for name, value := range msg.Args {
				placeholder := "$" + name
				content = strings.ReplaceAll(content, placeholder, value)
			}
		}

		// Handle custom command execution
		cmd := p.sendMessage(content, nil)
		if cmd != nil {
			return p, cmd
		}
	case chat.SessionSelectedMsg:
		if p.session.ID == "" {
			cmd := p.setSidebar()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		p.session = msg
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keyMap.ShowCompletionDialog):
			p.showCompletionDialog = true
			// Continue sending keys to layout->chat
		case key.Matches(msg, keyMap.NewSession):
			p.session = session.Session{}
			return p, tea.Batch(
				p.clearSidebar(),
				util.CmdHandler(chat.SessionClearedMsg{}),
			)
		case key.Matches(msg, keyMap.Cancel):
			if p.session.ID != "" && p.app != nil && p.app.CoderAgent != nil {
				// Cancel the current session's generation process
				// This allows users to interrupt long-running operations
				p.app.CoderAgent.Cancel(p.session.ID)
				return p, nil
			}
		}
	}
	if p.showCompletionDialog {
		context, contextCmd := p.completionDialog.Update(msg)
		p.completionDialog = context.(dialog.CompletionDialog)
		cmds = append(cmds, contextCmd)

		// Doesn't forward event if enter key is pressed
		if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
			if keyMsg.String() == "enter" {
				return p, tea.Batch(cmds...)
			}
		}
	}

	u, cmd := p.layout.Update(msg)
	cmds = append(cmds, cmd)
	p.layout = u.(layout.SplitPaneLayout)

	return p, tea.Batch(cmds...)
}

func (p *chatPage) setSidebar() tea.Cmd {
	var historySvc history.Service
	if p.app != nil {
		historySvc = p.app.History
	}
	sidebarContainer := layout.NewContainer(
		chat.NewSidebarCmp(p.session, historySvc),
		layout.WithPadding(1, 1, 1, 1),
		layout.WithBackgroundFunc(themeBackground),
		layout.WithBorderColorFunc(themeBorderNormal),
	)
	return tea.Batch(p.layout.SetRightPanel(sidebarContainer), sidebarContainer.Init())
}

func (p *chatPage) clearSidebar() tea.Cmd {
	return p.layout.ClearRightPanel()
}

func (p *chatPage) sendMessage(text string, attachments []message.Attachment) tea.Cmd {
	if p.app == nil {
		return nil
	}
	var cmds []tea.Cmd
	if p.session.ID == "" {
		sess, err := p.app.Sessions.Create(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}

		p.session = sess
		cmd := p.setSidebar()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		cmds = append(cmds, util.CmdHandler(chat.SessionSelectedMsg(sess)))
	}

	_, err := p.app.CoderAgent.Run(context.Background(), p.session.ID, text, attachments...)
	if err != nil {
		return util.ReportError(err)
	}
	return tea.Batch(cmds...)
}

func (p *chatPage) SetSize(width, height int) tea.Cmd {
	return p.layout.SetSize(width, height)
}

func (p *chatPage) GetSize() (int, int) {
	return p.layout.GetSize()
}

// Draw renders the chat split pane layout (messages, optional sidebar, editor)
// directly onto scr, overlays the completion dialog right above the editor when
// open, and returns the editor's hardware cursor.
func (p *chatPage) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	cur := p.layout.Draw(scr, area)

	if p.showCompletionDialog {
		editorWidth, editorHeight := p.editor.GetSize()
		compW := editorWidth
		if compW <= 0 || compW > area.Dx() {
			compW = area.Dx()
		}
		p.completionDialog.SetWidth(compW)

		availHeight := max(0, area.Dy()-editorHeight)
		if availHeight > 0 && compW > 0 {
			popupArea := image.Rect(
				area.Min.X,
				area.Min.Y,
				area.Min.X+compW,
				area.Max.Y-editorHeight,
			)
			p.completionDialog.Draw(scr, popupArea)
		}
	}

	return cur
}

// View renders the chat page via a temporary Ultraviolet screen buffer so View
// and Draw produce identical output, preserving the hardware cursor.
func (p *chatPage) View() tea.View {
	w, h := p.layout.GetSize()
	buf := uv.NewScreenBuffer(max(1, w), max(1, h))
	cur := p.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (p *chatPage) BindingKeys() []key.Binding {
	bindings := layout.KeyMapToSlice(keyMap)
	bindings = append(bindings, p.messages.BindingKeys()...)
	bindings = append(bindings, p.editor.BindingKeys()...)
	return bindings
}

// ChatRenderState returns render-affecting transcript state for frame caching.
func (p *chatPage) ChatRenderState() chat.RenderState {
	if p.messagesCmp == nil {
		return chat.RenderState{}
	}
	return p.messagesCmp.RenderState()
}

// BeginResize delegates live-resize suppression and settle scheduling to the
// underlying messages component.
func (p *chatPage) BeginResize() tea.Cmd {
	if p.messagesCmp == nil {
		return nil
	}
	return p.messagesCmp.BeginResize()
}

// WarmStep advances incremental post-resize item cache warming.
func (p *chatPage) WarmStep(seq int) (tea.Cmd, bool) {
	if p.messagesCmp == nil {
		return nil, false
	}
	return p.messagesCmp.WarmStep(seq)
}

// EnsureScrollbarHide arms the transcript scrollbar auto-hide timer if needed.
func (p *chatPage) EnsureScrollbarHide() tea.Cmd {
	if p.messagesCmp == nil {
		return nil
	}
	return p.messagesCmp.EnsureScrollbarHide()
}

// Restyle clears cached item styles after a theme change.
func (p *chatPage) Restyle() {
	if p.messagesCmp == nil {
		return
	}
	p.messagesCmp.Restyle()
}

// IsAnimating reports whether the chat transcript has an active animation.
func (p *chatPage) IsAnimating() bool {
	if p.messagesCmp == nil {
		return false
	}
	return p.messagesCmp.IsAnimating()
}

// AdvanceAnimation advances the transcript working spinner by one frame.
func (p *chatPage) AdvanceAnimation() bool {
	if p.messagesCmp == nil {
		return false
	}
	return p.messagesCmp.AdvanceAnimation()
}

// HandleWheel routes coalesced mouse wheel events to the chat transcript.
func (p *chatPage) HandleWheel(msg render.CoalescedWheelMsg) bool {
	if p.messagesCmp == nil {
		return false
	}
	return p.messagesCmp.HandleWheel(msg)
}

// CompletionOpen reports whether the inline completion popup is currently shown.
func (p *chatPage) CompletionOpen() bool {
	return p.showCompletionDialog
}

func NewChatPage(app *app.App) tea.Model {
	cg := completions.NewFileAndFolderContextGroup()
	completionDialog := dialog.NewCompletionDialogCmp(cg)

	messagesModel := chat.NewMessagesCmp(app)
	messagesCmp, _ := messagesModel.(chat.MessagesCmp)

	messagesContainer := layout.NewContainer(
		messagesModel,
		layout.WithPadding(1, 1, 0, 1),
		layout.WithBackgroundFunc(themeBackground),
		layout.WithBorderColorFunc(themeBorderNormal),
	)
	editorContainer := layout.NewContainer(
		chat.NewEditorCmp(app),
		layout.WithBorder(true, false, false, false),
		layout.WithBackgroundFunc(themeBackground),
		layout.WithBorderColorFunc(themeBorderNormal),
	)
	return &chatPage{
		app:              app,
		editor:           editorContainer,
		messages:         messagesContainer,
		messagesCmp:      messagesCmp,
		completionDialog: completionDialog,
		layout: layout.NewSplitPane(
			layout.WithLeftPanel(messagesContainer),
			layout.WithBottomPanel(editorContainer),
			layout.WithSplitBackgroundFunc(themeBackground),
		),
	}
}
