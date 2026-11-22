package chat

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

type editorCmp struct {
	width       int
	height      int
	app         *app.App
	session     session.Session
	textarea    textarea.Model
	attachments []message.Attachment
	deleteMode  bool
}

var _ layout.Drawable = (*editorCmp)(nil)

type EditorKeyMaps struct {
	Send       key.Binding
	OpenEditor key.Binding
}

type bluredEditorKeyMaps struct {
	Send       key.Binding
	Focus      key.Binding
	OpenEditor key.Binding
}

type DeleteAttachmentKeyMaps struct {
	AttachmentDeleteMode key.Binding
	Escape               key.Binding
	DeleteAllAttachments key.Binding
}

var editorMaps = EditorKeyMaps{
	Send: key.NewBinding(
		key.WithKeys("enter", "ctrl+s"),
		key.WithHelp("enter", "send message"),
	),
	OpenEditor: key.NewBinding(
		key.WithKeys("ctrl+e"),
		key.WithHelp("ctrl+e", "open editor"),
	),
}

var DeleteKeyMaps = DeleteAttachmentKeyMaps{
	AttachmentDeleteMode: key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r+{i}", "delete attachment at index i"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "cancel delete mode"),
	),
	DeleteAllAttachments: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("ctrl+r+r", "delete all attchments"),
	),
}

const (
	maxAttachments    = 5
	editorPromptWidth = 2 // PaddingLeft(1) + ">"
)

func (m *editorCmp) openEditor() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}

	tmpfile, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return util.ReportError(err)
	}
	tmpfile.Close()
	c := exec.Command(editor, tmpfile.Name()) //nolint:gosec
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return util.ReportError(err)
		}
		content, err := os.ReadFile(tmpfile.Name())
		if err != nil {
			return util.ReportError(err)
		}
		if len(content) == 0 {
			return util.ReportWarn("Message is empty")
		}
		os.Remove(tmpfile.Name())
		attachments := m.attachments
		m.attachments = nil
		return SendMsg{
			Text:        string(content),
			Attachments: attachments,
		}
	})
}

func (m *editorCmp) Init() tea.Cmd {
	// Hardware cursor blinks natively in the terminal; no synthetic blink tick.
	return nil
}

func (m *editorCmp) isSessionBusy() bool {
	return m.app != nil && m.app.CoderAgent != nil && m.app.CoderAgent.IsSessionBusy(m.session.ID)
}

func (m *editorCmp) send() tea.Cmd {
	if m.isSessionBusy() {
		return util.ReportWarn("Agent is working, please wait...")
	}

	value := m.textarea.Value()
	m.textarea.Reset()
	attachments := m.attachments

	m.attachments = nil
	m.syncTextareaSize()
	if value == "" {
		return nil
	}
	return tea.Batch(
		util.CmdHandler(SendMsg{
			Text:        value,
			Attachments: attachments,
		}),
	)
}

func (m *editorCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case dialog.ThemeChangedMsg:
		m.textarea = CreateTextArea(&m.textarea)
		m.syncTextareaSize()
	case dialog.CompletionSelectedMsg:
		existingValue := m.textarea.Value()
		modifiedValue := strings.Replace(existingValue, msg.SearchString, msg.CompletionValue, 1)

		m.textarea.SetValue(modifiedValue)
		return m, nil
	case SessionSelectedMsg:
		if msg.ID != m.session.ID {
			m.session = msg
		}
		return m, nil
	case dialog.AttachmentAddedMsg:
		if len(m.attachments) >= maxAttachments {
			logging.ErrorPersist(fmt.Sprintf("cannot add more than %d images", maxAttachments))
			return m, cmd
		}
		m.attachments = append(m.attachments, msg.Attachment)
		m.syncTextareaSize()
	case tea.KeyPressMsg:
		if key.Matches(msg, DeleteKeyMaps.AttachmentDeleteMode) {
			m.deleteMode = true
			return m, nil
		}
		if key.Matches(msg, DeleteKeyMaps.DeleteAllAttachments) && m.deleteMode {
			m.deleteMode = false
			m.attachments = nil
			m.syncTextareaSize()
			return m, nil
		}
		if m.deleteMode && len(msg.Text) > 0 {
			r := []rune(msg.Text)[0]
			if unicode.IsDigit(r) {
				num := int(r - '0')
				m.deleteMode = false
				if num < 10 && len(m.attachments) > num {
					if num == 0 {
						m.attachments = m.attachments[num+1:]
					} else {
						m.attachments = slices.Delete(m.attachments, num, num+1)
					}
					m.syncTextareaSize()
					return m, nil
				}
			}
		}
		if key.Matches(msg, messageKeys.PageUp) || key.Matches(msg, messageKeys.PageDown) ||
			key.Matches(msg, messageKeys.HalfPageUp) || key.Matches(msg, messageKeys.HalfPageDown) {
			return m, nil
		}
		if key.Matches(msg, editorMaps.OpenEditor) {
			if m.isSessionBusy() {
				return m, util.ReportWarn("Agent is working, please wait...")
			}
			return m, m.openEditor()
		}
		if key.Matches(msg, DeleteKeyMaps.Escape) {
			m.deleteMode = false
			return m, nil
		}
		// Handle Enter / Ctrl+S key
		if m.textarea.Focused() && key.Matches(msg, editorMaps.Send) {
			value := m.textarea.Value()
			if len(value) > 0 && value[len(value)-1] == '\\' {
				// If the last character is a backslash, remove it and add a newline
				m.textarea.SetValue(value[:len(value)-1] + "\n")
				return m, nil
			}
			return m, m.send()
		}
	}
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *editorCmp) promptView() (string, int) {
	t := theme.CurrentTheme()
	prompt := styles.BaseStyle().
		Padding(0, 0, 0, 1).
		Bold(true).
		Foreground(t.Primary()).
		Render(">")
	return prompt, lipgloss.Width(prompt)
}

// Draw renders the optional attachments row, prompt, and textarea onto scr
// within area, returning the translated hardware cursor when focused.
func (m *editorCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	layout.ClearArea(scr, area)

	prompt, promptWidth := m.promptView()
	attachmentsOffset := 0
	if len(m.attachments) > 0 && area.Dy() > 1 {
		attachmentsOffset = 1
		attachArea := image.Rect(area.Min.X, area.Min.Y, area.Max.X, area.Min.Y+1)
		layout.DrawAt(scr, attachArea, m.attachmentsContent())
	}

	taWidth := max(1, area.Dx()-promptWidth)
	taHeight := max(1, area.Dy()-attachmentsOffset)
	if m.textarea.Width() != taWidth {
		m.textarea.SetWidth(taWidth)
	}
	if m.textarea.Height() != taHeight {
		m.textarea.SetHeight(taHeight)
	}

	promptMaxX := min(area.Max.X, area.Min.X+promptWidth)
	promptArea := image.Rect(area.Min.X, area.Min.Y+attachmentsOffset, promptMaxX, area.Max.Y)
	layout.DrawAt(scr, promptArea, prompt)

	taArea := image.Rect(promptMaxX, area.Min.Y+attachmentsOffset, area.Max.X, area.Max.Y)
	if !taArea.Empty() {
		layout.DrawAt(scr, taArea, m.textarea.View())
	}

	if !m.textarea.Focused() {
		return nil
	}
	cur := m.textarea.Cursor()
	if cur == nil {
		return nil
	}
	c := *cur
	c.X += area.Min.X + promptWidth
	c.Y += area.Min.Y + attachmentsOffset
	c.X = util.Clamp(c.X, area.Min.X, area.Max.X-1)
	c.Y = util.Clamp(c.Y, area.Min.Y, area.Max.Y-1)
	return &c
}

func (m *editorCmp) View() tea.View {
	if m.width <= 0 || m.height <= 0 {
		prompt, promptWidth := m.promptView()
		attachmentsOffset := 0
		var content string
		if len(m.attachments) == 0 {
			content = lipgloss.JoinHorizontal(lipgloss.Top, prompt, m.textarea.View())
		} else {
			attachmentsOffset = 1
			content = lipgloss.JoinVertical(
				lipgloss.Left,
				m.attachmentsContent(),
				lipgloss.JoinHorizontal(lipgloss.Top, prompt, m.textarea.View()),
			)
		}
		v := tea.NewView(content)
		if m.textarea.Focused() {
			if cur := m.textarea.Cursor(); cur != nil {
				c := *cur
				c.X += promptWidth
				c.Y += attachmentsOffset
				v.Cursor = &c
			}
		}
		return v
	}

	buf := uv.NewScreenBuffer(m.width, m.height)
	cur := m.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (m *editorCmp) syncTextareaSize() {
	if m.width <= 0 && m.height <= 0 {
		return
	}
	attachmentsOffset := 0
	if len(m.attachments) > 0 && m.height > 1 {
		attachmentsOffset = 1
	}
	if m.width > 0 {
		m.textarea.SetWidth(max(1, m.width-editorPromptWidth))
	}
	if m.height > 0 {
		m.textarea.SetHeight(max(1, m.height-attachmentsOffset))
	}
}

func (m *editorCmp) SetSize(width, height int) tea.Cmd {
	m.width = width
	m.height = height
	m.syncTextareaSize()
	return nil
}

func (m *editorCmp) GetSize() (int, int) {
	return m.textarea.Width(), m.textarea.Height()
}

func (m *editorCmp) attachmentsContent() string {
	var styledAttachments []string
	t := theme.CurrentTheme()
	attachmentStyles := styles.BaseStyle().
		MarginLeft(1).
		Background(t.TextMuted()).
		Foreground(t.Text())
	for i, attachment := range m.attachments {
		var filename string
		if len(attachment.FileName) > 10 {
			filename = fmt.Sprintf(" %s %s...", styles.DocumentIcon, attachment.FileName[0:7])
		} else {
			filename = fmt.Sprintf(" %s %s", styles.DocumentIcon, attachment.FileName)
		}
		if m.deleteMode {
			filename = fmt.Sprintf("%d%s", i, filename)
		}
		styledAttachments = append(styledAttachments, attachmentStyles.Render(filename))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, styledAttachments...)
}

func (m *editorCmp) BindingKeys() []key.Binding {
	bindings := []key.Binding{}
	bindings = append(bindings, layout.KeyMapToSlice(editorMaps)...)
	bindings = append(bindings, layout.KeyMapToSlice(DeleteKeyMaps)...)
	return bindings
}

func CreateTextArea(existing *textarea.Model) textarea.Model {
	t := theme.CurrentTheme()
	bgColor := t.Background()
	textColor := t.Text()
	textMutedColor := t.TextMuted()

	ta := textarea.New()
	taStyles := textarea.DefaultStyles(theme.HasDarkBackground())
	taStyles.Blurred.Base = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	taStyles.Blurred.CursorLine = styles.BaseStyle().Background(bgColor)
	taStyles.Blurred.Placeholder = styles.BaseStyle().Background(bgColor).Foreground(textMutedColor)
	taStyles.Blurred.Text = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	taStyles.Blurred.Prompt = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	taStyles.Focused.Base = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	taStyles.Focused.CursorLine = styles.BaseStyle().Background(bgColor)
	taStyles.Focused.Placeholder = styles.BaseStyle().Background(bgColor).Foreground(textMutedColor)
	taStyles.Focused.Text = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	taStyles.Focused.Prompt = styles.BaseStyle().Background(bgColor).Foreground(textColor)
	ta.SetStyles(taStyles)

	ta.Prompt = " "
	ta.ShowLineNumbers = false
	ta.CharLimit = -1
	ta.SetVirtualCursor(false)

	if existing != nil {
		ta.SetValue(existing.Value())
		ta.SetWidth(existing.Width())
		ta.SetHeight(existing.Height())
		if existing.Focused() {
			ta.Focus()
		} else {
			ta.Blur()
		}
		return ta
	}

	ta.Focus()
	return ta
}

func NewEditorCmp(app *app.App) tea.Model {
	ta := CreateTextArea(nil)
	return &editorCmp{
		app:      app,
		textarea: ta,
	}
}
