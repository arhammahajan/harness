package tui

import (
	"context"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	uvlayout "github.com/charmbracelet/ultraviolet/layout"
	"github.com/charmbracelet/ultraviolet/screen"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/config"
	"github.com/arhammahajan/harness/internal/llm/agent"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/lsp"
	"github.com/arhammahajan/harness/internal/permission"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/chat"
	"github.com/arhammahajan/harness/internal/tui/components/core"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/page"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

type keyMap struct {
	Logs          key.Binding
	Quit          key.Binding
	Help          key.Binding
	SwitchSession key.Binding
	Commands      key.Binding
	Filepicker    key.Binding
	Models        key.Binding
	SwitchTheme   key.Binding
}

type startCompactSessionMsg struct{}

const (
	quitKey = "q"
)

var keys = keyMap{
	Logs: key.NewBinding(
		key.WithKeys("ctrl+l"),
		key.WithHelp("ctrl+l", "logs"),
	),

	Quit: key.NewBinding(
		key.WithKeys("ctrl+c"),
		key.WithHelp("ctrl+c", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("ctrl+_", "ctrl+h"),
		key.WithHelp("ctrl+?", "toggle help"),
	),

	SwitchSession: key.NewBinding(
		key.WithKeys("ctrl+s"),
		key.WithHelp("ctrl+s", "switch session"),
	),

	Commands: key.NewBinding(
		key.WithKeys("ctrl+k"),
		key.WithHelp("ctrl+k", "commands"),
	),
	Filepicker: key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "select files to upload"),
	),
	Models: key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "model selection"),
	),

	SwitchTheme: key.NewBinding(
		key.WithKeys("ctrl+t"),
		key.WithHelp("ctrl+t", "switch theme"),
	),
}

var helpEsc = key.NewBinding(
	key.WithKeys("?"),
	key.WithHelp("?", "toggle help"),
)

var returnKey = key.NewBinding(
	key.WithKeys("esc"),
	key.WithHelp("esc", "close"),
)

var logsKeyReturnKey = key.NewBinding(
	key.WithKeys("esc", "backspace", quitKey),
	key.WithHelp("esc/q", "go back"),
)

// frameKey identifies a memoized rendered frame for the chat page.
type frameKey struct {
	width, height int
	page          page.PageID
	chat          chat.RenderState
}

type appModel struct {
	width, height   int
	currentPage     page.PageID
	previousPage    page.PageID
	pages           map[page.PageID]tea.Model
	loadedPages     map[page.PageID]bool
	status          core.StatusCmp
	app             *app.App
	selectedSession session.Session

	showPermissions bool
	permissions     dialog.PermissionDialogCmp

	showHelp bool
	help     dialog.HelpCmp

	showQuit bool
	quit     dialog.QuitDialog

	showSessionDialog bool
	sessionDialog     dialog.SessionDialog

	showCommandDialog bool
	commandDialog     dialog.CommandDialog
	commands          []dialog.Command

	showModelDialog bool
	modelDialog     dialog.ModelDialog

	showInitDialog bool
	initDialog     dialog.InitDialogCmp

	showFilepicker bool
	filepicker     dialog.FilepickerCmp

	showThemeDialog bool
	themeDialog     dialog.ThemeDialog

	showMultiArgumentsDialog bool
	multiArgumentsDialog     dialog.MultiArgumentsDialogCmp

	isCompacting      bool
	compactingMessage string

	// Crush rendering pipeline state: whole-frame memoization, shared
	// animation clock, and discovered terminal capabilities.
	frames           *render.FrameCache[frameKey]
	scrollOnlyUpdate bool
	frameDirty       bool
	frameSkipPut     bool
	frameGCArmed     bool
	animClock        render.AnimClock
	caps             render.Capabilities
}

func (a *appModel) chatPage() page.ChatPageModel {
	cp, _ := a.pages[page.ChatPage].(page.ChatPageModel)
	return cp
}

func (a *appModel) hasModalOpen() bool {
	return a.showPermissions ||
		a.showFilepicker ||
		a.isCompacting ||
		a.showHelp ||
		a.showQuit ||
		a.showSessionDialog ||
		a.showModelDialog ||
		a.showCommandDialog ||
		a.showInitDialog ||
		a.showThemeDialog ||
		a.showMultiArgumentsDialog
}

func (a *appModel) isAgentBusy() bool {
	return a.app != nil && a.app.CoderAgent != nil && a.app.CoderAgent.IsBusy()
}

func (a *appModel) markScrollOnly() {
	a.scrollOnlyUpdate = true
}

func (a *appModel) invalidateFrames() {
	a.frameDirty = true
}

func (a *appModel) beginFrameUpdate() {
	a.scrollOnlyUpdate = false
	a.frameSkipPut = false
}

func (a *appModel) frameCacheable() bool {
	if a.frames == nil || a.currentPage != page.ChatPage || a.hasModalOpen() {
		return false
	}
	if cp := a.chatPage(); cp != nil && cp.CompletionOpen() {
		return false
	}
	return a.width > 0 && a.height > 0
}

func (a *appModel) armFrameGC() tea.Cmd {
	if a.frames == nil || a.frameGCArmed {
		return nil
	}
	a.frameGCArmed = true
	return tea.Tick(a.frames.TTL(), func(time.Time) tea.Msg { return render.FrameGCMsg{} })
}

func (a *appModel) endFrameUpdate() tea.Cmd {
	if !a.scrollOnlyUpdate || !a.frameCacheable() {
		return nil
	}
	return a.armFrameGC()
}

func (a *appModel) handleFrameGC() {
	a.frameGCArmed = false
	if a.frames == nil {
		return
	}
	a.frames.GC(time.Now())
	if a.frames.Len() == 0 {
		a.frameSkipPut = true
		return
	}
	a.scrollOnlyUpdate = true
}

func (a *appModel) frameKeyNow() (frameKey, bool) {
	if !a.frameCacheable() {
		return frameKey{}, false
	}
	cp := a.chatPage()
	if cp == nil {
		return frameKey{}, false
	}
	return frameKey{
		width:  a.width,
		height: a.height,
		page:   a.currentPage,
		chat:   cp.ChatRenderState(),
	}, true
}

func (a *appModel) currentFrameKey() (frameKey, bool) {
	scrollOnly := a.scrollOnlyUpdate && !a.frameDirty
	a.scrollOnlyUpdate = false
	a.frameDirty = false
	if a.frames == nil {
		return frameKey{}, false
	}
	if !scrollOnly {
		a.frames.Reset()
	}
	return a.frameKeyNow()
}

func (a *appModel) storeFrame(key frameKey, content string, cursor *tea.Cursor) {
	if a.frameDirty {
		a.frameDirty = false
		a.frames.Reset()
		return
	}
	if a.frameSkipPut {
		a.frameSkipPut = false
		return
	}
	if now, ok := a.frameKeyNow(); !ok || now != key {
		return
	}
	a.frames.Put(key, content, cursor)
}

func (a *appModel) updateTail(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	if a.currentPage == page.ChatPage {
		if cp := a.chatPage(); cp != nil {
			if cmd := a.animClock.Ensure(func() bool { return cp.IsAnimating() }); cmd != nil {
				cmds = append(cmds, cmd)
			}
			if cmd := cp.EnsureScrollbarHide(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	if cmd := a.endFrameUpdate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return a, tea.Batch(cmds...)
}

func (a *appModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	cmd := a.pages[a.currentPage].Init()
	a.loadedPages[a.currentPage] = true
	cmds = append(cmds, cmd)
	cmds = append(cmds, a.status.Init())
	cmds = append(cmds, a.quit.Init())
	cmds = append(cmds, a.help.Init())
	cmds = append(cmds, a.sessionDialog.Init())
	cmds = append(cmds, a.commandDialog.Init())
	cmds = append(cmds, a.modelDialog.Init())
	cmds = append(cmds, a.initDialog.Init())
	cmds = append(cmds, a.filepicker.Init())
	cmds = append(cmds, a.themeDialog.Init())
	cmds = append(cmds, render.QueryCmd(uv.Environ(os.Environ())))

	// Check if we should show the init dialog
	cmds = append(cmds, func() tea.Msg {
		shouldShow, err := config.ShouldShowInitDialog()
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  "Failed to check init status: " + err.Error(),
			}
		}
		return dialog.ShowInitDialogMsg{Show: shouldShow}
	})

	return tea.Batch(cmds...)
}

func (a *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a.beginFrameUpdate()
	a.caps.Update(msg)

	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case render.FrameGCMsg:
		a.handleFrameGC()
		return a.updateTail(cmds)

	case render.AnimTickMsg:
		if !a.animClock.Accept(msg) || a.currentPage != page.ChatPage {
			a.markScrollOnly()
			return a.updateTail(cmds)
		}
		cp := a.chatPage()
		if cp == nil || !cp.AdvanceAnimation() {
			a.markScrollOnly()
			return a.updateTail(cmds)
		}
		return a.updateTail(cmds)

	case chat.WarmMsg:
		if cp := a.chatPage(); cp != nil {
			warmCmd, done := cp.WarmStep(msg.Seq)
			if warmCmd != nil {
				cmds = append(cmds, warmCmd)
			}
			if !done {
				a.markScrollOnly()
			} else {
				a.invalidateFrames()
			}
		}
		return a.updateTail(cmds)

	case tea.BackgroundColorMsg:
		theme.SetHasDarkBackground(msg.IsDark())
		if cp := a.chatPage(); cp != nil {
			cp.Restyle()
		}
		a.invalidateFrames()
		return a.updateTail(cmds)

	case render.CoalescedWheelMsg:
		if !a.hasModalOpen() {
			if a.currentPage == page.ChatPage {
				if cp := a.chatPage(); cp != nil {
					cp.HandleWheel(msg)
					a.markScrollOnly()
					return a.updateTail(cmds)
				}
			}
			a.pages[a.currentPage], cmd = a.pages[a.currentPage].Update(msg)
			cmds = append(cmds, cmd)
			return a.updateTail(cmds)
		}

	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.invalidateFrames()

		pageSizeMsg := tea.WindowSizeMsg{
			Width:  msg.Width,
			Height: max(0, msg.Height-1), // Make space for the status bar
		}

		if a.currentPage == page.ChatPage {
			if cp := a.chatPage(); cp != nil {
				cmds = append(cmds, cp.BeginResize())
			}
		}

		s, _ := a.status.Update(msg)
		a.status = s.(core.StatusCmp)
		a.pages[a.currentPage], cmd = a.pages[a.currentPage].Update(pageSizeMsg)
		cmds = append(cmds, cmd)

		prm, permCmd := a.permissions.Update(pageSizeMsg)
		a.permissions = prm.(dialog.PermissionDialogCmp)
		cmds = append(cmds, permCmd)

		help, helpCmd := a.help.Update(pageSizeMsg)
		a.help = help.(dialog.HelpCmp)
		cmds = append(cmds, helpCmd)

		sess, sessionCmd := a.sessionDialog.Update(pageSizeMsg)
		a.sessionDialog = sess.(dialog.SessionDialog)
		cmds = append(cmds, sessionCmd)

		command, commandCmd := a.commandDialog.Update(pageSizeMsg)
		a.commandDialog = command.(dialog.CommandDialog)
		cmds = append(cmds, commandCmd)

		filepicker, filepickerCmd := a.filepicker.Update(pageSizeMsg)
		a.filepicker = filepicker.(dialog.FilepickerCmp)
		cmds = append(cmds, filepickerCmd)

		a.initDialog.SetSize(pageSizeMsg.Width, pageSizeMsg.Height)

		if a.showMultiArgumentsDialog {
			a.multiArgumentsDialog.SetSize(pageSizeMsg.Width, pageSizeMsg.Height)
			args, argsCmd := a.multiArgumentsDialog.Update(pageSizeMsg)
			a.multiArgumentsDialog = args.(dialog.MultiArgumentsDialogCmp)
			cmds = append(cmds, argsCmd, a.multiArgumentsDialog.Init())
		}

		return a.updateTail(cmds)

	// Status
	case util.InfoMsg:
		s, cmd := a.status.Update(msg)
		a.status = s.(core.StatusCmp)
		cmds = append(cmds, cmd)
		return a.updateTail(cmds)

	case pubsub.Event[logging.LogMessage]:
		if msg.Payload.Persist {
			switch msg.Payload.Level {
			case "error":
				s, cmd := a.status.Update(util.InfoMsg{
					Type: util.InfoTypeError,
					Msg:  msg.Payload.Message,
					TTL:  msg.Payload.PersistTime,
				})
				a.status = s.(core.StatusCmp)
				cmds = append(cmds, cmd)
			case "info":
				s, cmd := a.status.Update(util.InfoMsg{
					Type: util.InfoTypeInfo,
					Msg:  msg.Payload.Message,
					TTL:  msg.Payload.PersistTime,
				})
				a.status = s.(core.StatusCmp)
				cmds = append(cmds, cmd)
			case "warn":
				s, cmd := a.status.Update(util.InfoMsg{
					Type: util.InfoTypeWarn,
					Msg:  msg.Payload.Message,
					TTL:  msg.Payload.PersistTime,
				})
				a.status = s.(core.StatusCmp)
				cmds = append(cmds, cmd)
			default:
				s, cmd := a.status.Update(util.InfoMsg{
					Type: util.InfoTypeInfo,
					Msg:  msg.Payload.Message,
					TTL:  msg.Payload.PersistTime,
				})
				a.status = s.(core.StatusCmp)
				cmds = append(cmds, cmd)
			}
		}

	case util.ClearStatusMsg:
		s, _ := a.status.Update(msg)
		a.status = s.(core.StatusCmp)

	// Permission
	case pubsub.Event[permission.PermissionRequest]:
		a.showPermissions = true
		a.invalidateFrames()
		cmds = append(cmds, a.permissions.SetPermissions(msg.Payload))
		return a.updateTail(cmds)

	case dialog.PermissionResponseMsg:
		if a.app != nil && a.app.Permissions != nil {
			switch msg.Action {
			case dialog.PermissionAllow:
				a.app.Permissions.Grant(msg.Permission)
			case dialog.PermissionAllowForSession:
				a.app.Permissions.GrantPersistant(msg.Permission)
			case dialog.PermissionDeny:
				a.app.Permissions.Deny(msg.Permission)
			}
		}
		a.showPermissions = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case page.PageChangeMsg:
		a.invalidateFrames()
		cmds = append(cmds, a.moveToPage(msg.ID))
		return a.updateTail(cmds)

	case dialog.CloseQuitMsg:
		a.showQuit = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case dialog.CloseSessionDialogMsg:
		a.showSessionDialog = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case dialog.CloseCommandDialogMsg:
		a.showCommandDialog = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case startCompactSessionMsg:
		a.isCompacting = true
		a.compactingMessage = "Starting summarization..."
		a.invalidateFrames()

		if a.selectedSession.ID == "" {
			a.isCompacting = false
			cmds = append(cmds, util.ReportWarn("No active session to summarize"))
			return a.updateTail(cmds)
		}

		cmds = append(cmds, func() tea.Msg {
			ctx := context.Background()
			if a.app != nil && a.app.CoderAgent != nil {
				a.app.CoderAgent.Summarize(ctx, a.selectedSession.ID)
			}
			return nil
		})
		return a.updateTail(cmds)

	case pubsub.Event[agent.AgentEvent]:
		payload := msg.Payload
		if payload.Error != nil {
			a.isCompacting = false
			cmds = append(cmds, util.ReportError(payload.Error))
			return a.updateTail(cmds)
		}

		a.compactingMessage = payload.Progress

		if payload.Done && payload.Type == agent.AgentEventTypeSummarize {
			a.isCompacting = false
			cmds = append(cmds, util.ReportInfo("Session summarization complete"))
			return a.updateTail(cmds)
		} else if payload.Done && payload.Type == agent.AgentEventTypeResponse && a.selectedSession.ID != "" {
			if a.app != nil && a.app.CoderAgent != nil {
				model := a.app.CoderAgent.Model()
				contextWindow := model.ContextWindow
				tokens := a.selectedSession.CompletionTokens + a.selectedSession.PromptTokens
				if cfg := config.Get(); cfg != nil && (tokens >= int64(float64(contextWindow)*0.95)) && cfg.AutoCompact {
					cmds = append(cmds, util.CmdHandler(startCompactSessionMsg{}))
					return a.updateTail(cmds)
				}
			}
		}
		return a.updateTail(cmds)

	case dialog.CloseThemeDialogMsg:
		a.showThemeDialog = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case dialog.ThemeChangedMsg:
		if cp := a.chatPage(); cp != nil {
			cp.Restyle()
		}
		a.pages[a.currentPage], cmd = a.pages[a.currentPage].Update(msg)
		a.showThemeDialog = false
		a.invalidateFrames()
		cmds = append(cmds, cmd, util.ReportInfo("Theme changed to: "+msg.ThemeName))
		return a.updateTail(cmds)

	case dialog.CloseModelDialogMsg:
		a.showModelDialog = false
		a.invalidateFrames()
		return a.updateTail(cmds)

	case dialog.ModelSelectedMsg:
		a.showModelDialog = false
		a.invalidateFrames()
		if a.app != nil && a.app.CoderAgent != nil {
			model, err := a.app.CoderAgent.Update(config.AgentCoder, msg.Model.ID)
			if err != nil {
				cmds = append(cmds, util.ReportError(err))
				return a.updateTail(cmds)
			}
			cmds = append(cmds, util.ReportInfo(fmt.Sprintf("Model changed to %s", model.Name)))
		}
		return a.updateTail(cmds)

	case dialog.ShowInitDialogMsg:
		a.showInitDialog = msg.Show
		a.invalidateFrames()
		return a.updateTail(cmds)

	case dialog.CloseInitDialogMsg:
		a.showInitDialog = false
		a.invalidateFrames()
		if msg.Initialize {
			for _, c := range a.commands {
				if c.ID == "init" {
					if err := config.MarkProjectInitialized(); err != nil {
						cmds = append(cmds, util.ReportError(err))
						return a.updateTail(cmds)
					}
					cmds = append(cmds, c.Handler(c))
					return a.updateTail(cmds)
				}
			}
		} else {
			if err := config.MarkProjectInitialized(); err != nil {
				cmds = append(cmds, util.ReportError(err))
				return a.updateTail(cmds)
			}
		}
		return a.updateTail(cmds)

	case chat.SessionSelectedMsg:
		a.selectedSession = msg
		a.sessionDialog.SetSelectedSession(msg.ID)
		a.invalidateFrames()

	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.UpdatedEvent && msg.Payload.ID == a.selectedSession.ID {
			a.selectedSession = msg.Payload
		}

	case dialog.SessionSelectedMsg:
		a.showSessionDialog = false
		a.invalidateFrames()
		if a.currentPage == page.ChatPage {
			cmds = append(cmds, util.CmdHandler(chat.SessionSelectedMsg(msg.Session)))
		}
		return a.updateTail(cmds)

	case dialog.CommandSelectedMsg:
		a.showCommandDialog = false
		a.invalidateFrames()
		if msg.Command.Handler != nil {
			cmds = append(cmds, msg.Command.Handler(msg.Command))
			return a.updateTail(cmds)
		}
		cmds = append(cmds, util.ReportInfo("Command selected: "+msg.Command.Title))
		return a.updateTail(cmds)

	case dialog.ShowMultiArgumentsDialogMsg:
		a.multiArgumentsDialog = dialog.NewMultiArgumentsDialogCmp(msg.CommandID, msg.Content, msg.ArgNames)
		a.multiArgumentsDialog.SetSize(a.width, max(0, a.height-1))
		a.showMultiArgumentsDialog = true
		a.invalidateFrames()
		cmds = append(cmds, a.multiArgumentsDialog.Init())
		return a.updateTail(cmds)

	case dialog.CloseMultiArgumentsDialogMsg:
		a.showMultiArgumentsDialog = false
		a.invalidateFrames()
		if msg.Submit {
			content := msg.Content
			for name, value := range msg.Args {
				placeholder := "$" + name
				content = strings.ReplaceAll(content, placeholder, value)
			}
			cmds = append(cmds, util.CmdHandler(dialog.CommandRunCustomMsg{
				Content: content,
				Args:    msg.Args,
			}))
		}
		return a.updateTail(cmds)

	case tea.KeyPressMsg:
		if a.showMultiArgumentsDialog {
			args, cmd := a.multiArgumentsDialog.Update(msg)
			a.multiArgumentsDialog = args.(dialog.MultiArgumentsDialogCmp)
			cmds = append(cmds, cmd)
			return a.updateTail(cmds)
		}

		switch {
		case key.Matches(msg, keys.Quit):
			a.showQuit = !a.showQuit
			a.invalidateFrames()
			if a.showHelp {
				a.showHelp = false
			}
			if a.showSessionDialog {
				a.showSessionDialog = false
			}
			if a.showCommandDialog {
				a.showCommandDialog = false
			}
			if a.showFilepicker {
				a.showFilepicker = false
				a.filepicker.ToggleFilepicker(a.showFilepicker)
			}
			if a.showModelDialog {
				a.showModelDialog = false
			}
			if a.showMultiArgumentsDialog {
				a.showMultiArgumentsDialog = false
			}
			return a.updateTail(cmds)

		case key.Matches(msg, keys.SwitchSession):
			if a.currentPage == page.ChatPage && !a.showQuit && !a.showPermissions && !a.showCommandDialog {
				if a.app == nil || a.app.Sessions == nil {
					cmds = append(cmds, util.ReportWarn("No sessions available"))
					return a.updateTail(cmds)
				}
				sessions, err := a.app.Sessions.List(context.Background())
				if err != nil {
					cmds = append(cmds, util.ReportError(err))
					return a.updateTail(cmds)
				}
				if len(sessions) == 0 {
					cmds = append(cmds, util.ReportWarn("No sessions available"))
					return a.updateTail(cmds)
				}
				a.sessionDialog.SetSessions(sessions)
				a.showSessionDialog = true
				a.invalidateFrames()
				return a.updateTail(cmds)
			}
			return a.updateTail(cmds)

		case key.Matches(msg, keys.Commands):
			if a.currentPage == page.ChatPage && !a.showQuit && !a.showPermissions && !a.showSessionDialog && !a.showThemeDialog && !a.showFilepicker {
				if len(a.commands) == 0 {
					cmds = append(cmds, util.ReportWarn("No commands available"))
					return a.updateTail(cmds)
				}
				a.commandDialog.SetCommands(a.commands)
				a.showCommandDialog = true
				a.invalidateFrames()
				return a.updateTail(cmds)
			}
			return a.updateTail(cmds)

		case key.Matches(msg, keys.Models):
			if a.showModelDialog {
				a.showModelDialog = false
				a.invalidateFrames()
				return a.updateTail(cmds)
			}
			if a.currentPage == page.ChatPage && !a.showQuit && !a.showPermissions && !a.showSessionDialog && !a.showCommandDialog {
				a.showModelDialog = true
				a.invalidateFrames()
				return a.updateTail(cmds)
			}
			return a.updateTail(cmds)

		case key.Matches(msg, keys.SwitchTheme):
			if !a.showQuit && !a.showPermissions && !a.showSessionDialog && !a.showCommandDialog {
				a.showThemeDialog = true
				a.invalidateFrames()
				cmds = append(cmds, a.themeDialog.Init())
				return a.updateTail(cmds)
			}
			return a.updateTail(cmds)

		case key.Matches(msg, returnKey) || msg.String() == quitKey:
			if msg.String() == quitKey {
				if a.currentPage == page.LogsPage {
					cmds = append(cmds, a.moveToPage(page.ChatPage))
					return a.updateTail(cmds)
				}
			} else if !a.filepicker.IsCWDFocused() {
				if a.showQuit {
					a.showQuit = !a.showQuit
					a.invalidateFrames()
					return a.updateTail(cmds)
				}
				if a.showHelp {
					a.showHelp = !a.showHelp
					a.invalidateFrames()
					return a.updateTail(cmds)
				}
				if a.showInitDialog {
					a.showInitDialog = false
					a.invalidateFrames()
					if err := config.MarkProjectInitialized(); err != nil {
						cmds = append(cmds, util.ReportError(err))
					}
					return a.updateTail(cmds)
				}
				if a.showFilepicker {
					a.showFilepicker = false
					a.filepicker.ToggleFilepicker(a.showFilepicker)
					a.invalidateFrames()
					return a.updateTail(cmds)
				}
				if a.currentPage == page.LogsPage {
					cmds = append(cmds, a.moveToPage(page.ChatPage))
					return a.updateTail(cmds)
				}
			}

		case key.Matches(msg, keys.Logs):
			cmds = append(cmds, a.moveToPage(page.LogsPage))
			return a.updateTail(cmds)

		case key.Matches(msg, keys.Help):
			if a.showQuit {
				return a.updateTail(cmds)
			}
			a.showHelp = !a.showHelp
			a.invalidateFrames()
			return a.updateTail(cmds)

		case key.Matches(msg, helpEsc):
			if a.isAgentBusy() {
				if a.showQuit {
					return a.updateTail(cmds)
				}
				a.showHelp = !a.showHelp
				a.invalidateFrames()
				return a.updateTail(cmds)
			}

		case key.Matches(msg, keys.Filepicker):
			a.showFilepicker = !a.showFilepicker
			a.filepicker.ToggleFilepicker(a.showFilepicker)
			a.invalidateFrames()
			return a.updateTail(cmds)
		}

	default:
		f, filepickerCmd := a.filepicker.Update(msg)
		a.filepicker = f.(dialog.FilepickerCmp)
		cmds = append(cmds, filepickerCmd)
	}

	if a.showFilepicker {
		f, filepickerCmd := a.filepicker.Update(msg)
		a.filepicker = f.(dialog.FilepickerCmp)
		cmds = append(cmds, filepickerCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showQuit {
		q, quitCmd := a.quit.Update(msg)
		a.quit = q.(dialog.QuitDialog)
		cmds = append(cmds, quitCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}
	if a.showPermissions {
		d, permissionsCmd := a.permissions.Update(msg)
		a.permissions = d.(dialog.PermissionDialogCmp)
		cmds = append(cmds, permissionsCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showSessionDialog {
		d, sessionCmd := a.sessionDialog.Update(msg)
		a.sessionDialog = d.(dialog.SessionDialog)
		cmds = append(cmds, sessionCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showCommandDialog {
		d, commandCmd := a.commandDialog.Update(msg)
		a.commandDialog = d.(dialog.CommandDialog)
		cmds = append(cmds, commandCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showModelDialog {
		d, modelCmd := a.modelDialog.Update(msg)
		a.modelDialog = d.(dialog.ModelDialog)
		cmds = append(cmds, modelCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showInitDialog {
		d, initCmd := a.initDialog.Update(msg)
		a.initDialog = d.(dialog.InitDialogCmp)
		cmds = append(cmds, initCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	if a.showThemeDialog {
		d, themeCmd := a.themeDialog.Update(msg)
		a.themeDialog = d.(dialog.ThemeDialog)
		cmds = append(cmds, themeCmd)
		if _, ok := msg.(tea.KeyPressMsg); ok {
			return a.updateTail(cmds)
		}
	}

	s, _ := a.status.Update(msg)
	a.status = s.(core.StatusCmp)
	a.pages[a.currentPage], cmd = a.pages[a.currentPage].Update(msg)
	cmds = append(cmds, cmd)

	// Mark pure chat scroll key presses as scroll-only so the whole-frame
	// cache can serve revisited scroll positions directly from memory.
	if km, ok := msg.(tea.KeyPressMsg); ok && a.currentPage == page.ChatPage && !a.hasModalOpen() {
		switch km.String() {
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			a.markScrollOnly()
		}
	}

	return a.updateTail(cmds)
}

// RegisterCommand adds a command to the command dialog.
func (a *appModel) RegisterCommand(cmd dialog.Command) {
	a.commands = append(a.commands, cmd)
}

func (a *appModel) findCommand(id string) (dialog.Command, bool) {
	for _, cmd := range a.commands {
		if cmd.ID == id {
			return cmd, true
		}
	}
	return dialog.Command{}, false
}

func (a *appModel) moveToPage(pageID page.PageID) tea.Cmd {
	if a.isAgentBusy() {
		return util.ReportWarn("Agent is busy, please wait...")
	}

	var cmds []tea.Cmd
	if _, ok := a.loadedPages[pageID]; !ok {
		cmd := a.pages[pageID].Init()
		cmds = append(cmds, cmd)
		a.loadedPages[pageID] = true
	}
	a.previousPage = a.currentPage
	a.currentPage = pageID
	a.invalidateFrames()
	if sizable, ok := a.pages[a.currentPage].(layout.Sizeable); ok {
		cmd := sizable.SetSize(a.width, max(0, a.height-1))
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

// Draw implements [layout.Drawable] and draws the root UI onto scr.
func (a *appModel) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	screen.Clear(scr)

	var pageRect, statusRect image.Rectangle
	uvlayout.Vertical(
		uvlayout.Fill(1),
		uvlayout.Len(1),
	).Split(area).Assign(&pageRect, &statusRect)

	var cur *tea.Cursor
	if p := a.pages[a.currentPage]; p != nil {
		if drawable, ok := p.(layout.Drawable); ok {
			cur = drawable.Draw(scr, pageRect)
		} else {
			v := p.View()
			layout.DrawAt(scr, pageRect, v.Content)
			if v.Cursor != nil {
				c := *v.Cursor
				c.X += pageRect.Min.X
				c.Y += pageRect.Min.Y
				cur = &c
			}
		}
	}

	a.status.Draw(scr, statusRect)

	bounds := scr.Bounds()

	if a.showPermissions {
		cur = a.permissions.Draw(scr, bounds)
	}

	if a.showFilepicker {
		cur = a.filepicker.Draw(scr, bounds)
	}

	if a.isCompacting {
		t := theme.CurrentTheme()
		style := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.BorderFocused()).
			BorderBackground(t.Background()).
			Padding(1, 2).
			Background(t.Background()).
			Foreground(t.Text())

		overlay := style.Render("Summarizing\n" + a.compactingMessage)
		layout.DrawCenter(scr, bounds, overlay)
		cur = nil
	}

	if a.showHelp {
		bindings := layout.KeyMapToSlice(keys)
		if p, ok := a.pages[a.currentPage].(layout.Bindings); ok {
			bindings = append(bindings, p.BindingKeys()...)
		}
		if a.showPermissions {
			bindings = append(bindings, a.permissions.BindingKeys()...)
		}
		if a.currentPage == page.LogsPage {
			bindings = append(bindings, logsKeyReturnKey)
		}
		if !a.isAgentBusy() {
			bindings = append(bindings, helpEsc)
		}
		a.help.SetBindings(bindings)
		cur = a.help.Draw(scr, bounds)
	}

	if a.showQuit {
		cur = a.quit.Draw(scr, bounds)
	}

	if a.showSessionDialog {
		cur = a.sessionDialog.Draw(scr, bounds)
	}

	if a.showModelDialog {
		cur = a.modelDialog.Draw(scr, bounds)
	}

	if a.showCommandDialog {
		cur = a.commandDialog.Draw(scr, bounds)
	}

	if a.showInitDialog {
		cur = a.initDialog.Draw(scr, bounds)
	}

	if a.showThemeDialog {
		cur = a.themeDialog.Draw(scr, bounds)
	}

	if a.showMultiArgumentsDialog {
		cur = a.multiArgumentsDialog.Draw(scr, bounds)
	}

	return cur
}

func (a *appModel) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if t := theme.CurrentTheme(); t != nil {
		v.BackgroundColor = t.Background()
	}
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = a.caps.ReportFocusEvents
	v.WindowTitle = "harness"

	key, cacheable := a.currentFrameKey()
	if cacheable {
		if content, cursor, ok := a.frames.Get(key); ok {
			v.Content = content
			v.Cursor = cursor
			return v
		}
	}

	w, h := a.width, a.height
	if w <= 0 || h <= 0 {
		return v
	}

	canvas := uv.NewScreenBuffer(w, h)
	v.Cursor = a.Draw(canvas, canvas.Bounds())

	content := strings.ReplaceAll(canvas.Render(), "\r\n", "\n")
	contentLines := strings.Split(content, "\n")
	for i, line := range contentLines {
		contentLines[i] = strings.TrimRight(line, " ")
	}
	content = strings.Join(contentLines, "\n")

	v.Content = content
	if cacheable {
		a.storeFrame(key, content, v.Cursor)
	}
	return v
}

func New(app *app.App) tea.Model {
	startPage := page.ChatPage
	var lspClients map[string]*lsp.Client
	if app != nil {
		lspClients = app.LSPClients
	}
	model := &appModel{
		currentPage:   startPage,
		loadedPages:   make(map[page.PageID]bool),
		status:        core.NewStatusCmp(lspClients),
		help:          dialog.NewHelpCmp(),
		quit:          dialog.NewQuitCmp(),
		sessionDialog: dialog.NewSessionDialogCmp(),
		commandDialog: dialog.NewCommandDialogCmp(),
		modelDialog:   dialog.NewModelDialogCmp(),
		permissions:   dialog.NewPermissionDialogCmp(),
		initDialog:    dialog.NewInitDialogCmp(),
		themeDialog:   dialog.NewThemeDialogCmp(),
		app:           app,
		commands:      []dialog.Command{},
		pages: map[page.PageID]tea.Model{
			page.ChatPage: page.NewChatPage(app),
			page.LogsPage: page.NewLogsPage(),
		},
		filepicker: dialog.NewFilepickerCmp(app),
		frames:     render.NewFrameCache[frameKey](render.DefaultFrameCacheTTL, render.DefaultFrameCacheMaxEntries),
	}

	model.RegisterCommand(dialog.Command{
		ID:          "init",
		Title:       "Initialize Project",
		Description: "Create/Update the Harness.md memory file",
		Handler: func(cmd dialog.Command) tea.Cmd {
			prompt := `Please analyze this codebase and create a Harness.md file containing:
1. Build/lint/test commands - especially for running a single test
2. Code style guidelines including imports, formatting, types, naming conventions, error handling, etc.

The file you create will be given to agentic coding agents (such as yourself) that operate in this repository. Make it about 20 lines long.
If there's already a harness.md, improve it.
If there are Cursor rules (in .cursor/rules/ or .cursorrules) or Copilot rules (in .github/copilot-instructions.md), make sure to include them.`
			return tea.Batch(
				util.CmdHandler(chat.SendMsg{
					Text: prompt,
				}),
			)
		},
	})

	model.RegisterCommand(dialog.Command{
		ID:          "compact",
		Title:       "Compact Session",
		Description: "Summarize the current session and create a new one with the summary",
		Handler: func(cmd dialog.Command) tea.Cmd {
			return func() tea.Msg {
				return startCompactSessionMsg{}
			}
		},
	})

	// Load custom commands
	customCommands, err := dialog.LoadCustomCommands()
	if err != nil {
		logging.Warn("Failed to load custom commands", "error", err)
	} else {
		for _, cmd := range customCommands {
			model.RegisterCommand(cmd)
		}
	}

	return model
}
