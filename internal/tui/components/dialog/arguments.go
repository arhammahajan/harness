package dialog

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

type argumentsDialogKeyMap struct {
	Enter  key.Binding
	Escape key.Binding
}

// ShortHelp implements key.Map.
func (k argumentsDialogKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "confirm"),
		),
		key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
	}
}

// FullHelp implements key.Map.
func (k argumentsDialogKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{k.ShortHelp()}
}

// ShowMultiArgumentsDialogMsg is a message that is sent to show the multi-arguments dialog.
type ShowMultiArgumentsDialogMsg struct {
	CommandID string
	Content   string
	ArgNames  []string
}

// CloseMultiArgumentsDialogMsg is a message that is sent when the multi-arguments dialog is closed.
type CloseMultiArgumentsDialogMsg struct {
	Submit    bool
	CommandID string
	Content   string
	Args      map[string]string
}

// MultiArgumentsDialogCmp is a component that asks the user for multiple command arguments.
type MultiArgumentsDialogCmp struct {
	width, height int
	inputs        []textinput.Model
	focusIndex    int
	keys          argumentsDialogKeyMap
	commandID     string
	content       string
	argNames      []string
}

func argumentInputStyles(ti textinput.Model, t theme.Theme) textinput.Styles {
	s := ti.Styles()
	s.Focused.Placeholder = s.Focused.Placeholder.Background(t.Background())
	s.Focused.Prompt = s.Focused.Prompt.Background(t.Background()).Foreground(t.Primary())
	s.Focused.Text = s.Focused.Text.Background(t.Background()).Foreground(t.Primary())
	s.Blurred.Placeholder = s.Blurred.Placeholder.Background(t.Background())
	s.Blurred.Prompt = s.Blurred.Prompt.Background(t.Background())
	s.Blurred.Text = s.Blurred.Text.Background(t.Background())
	s.Cursor.Color = t.Primary()
	return s
}

// NewMultiArgumentsDialogCmp creates a new MultiArgumentsDialogCmp.
func NewMultiArgumentsDialogCmp(commandID, content string, argNames []string) MultiArgumentsDialogCmp {
	t := theme.CurrentTheme()
	inputs := make([]textinput.Model, len(argNames))

	for i, name := range argNames {
		ti := textinput.New()
		ti.Placeholder = fmt.Sprintf("Enter value for %s...", name)
		ti.SetWidth(40)
		ti.Prompt = ""
		ti.SetVirtualCursor(false)
		ti.SetStyles(argumentInputStyles(ti, t))

		// Only focus the first input initially
		if i == 0 {
			ti.Focus()
		} else {
			ti.Blur()
		}

		inputs[i] = ti
	}

	return MultiArgumentsDialogCmp{
		inputs:     inputs,
		keys:       argumentsDialogKeyMap{},
		commandID:  commandID,
		content:    content,
		argNames:   argNames,
		focusIndex: 0,
	}
}

// Init implements tea.Model.
func (m MultiArgumentsDialogCmp) Init() tea.Cmd {
	// Make sure only the first input is focused
	for i := range m.inputs {
		if i == 0 {
			m.inputs[i].Focus()
		} else {
			m.inputs[i].Blur()
		}
	}

	return textinput.Blink
}

// Update implements tea.Model.
func (m MultiArgumentsDialogCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	t := theme.CurrentTheme()

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			return m, util.CmdHandler(CloseMultiArgumentsDialogMsg{
				Submit:    false,
				CommandID: m.commandID,
				Content:   m.content,
				Args:      nil,
			})
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if len(m.inputs) == 0 || m.focusIndex == len(m.inputs)-1 {
				args := make(map[string]string)
				for i, name := range m.argNames {
					args[name] = m.inputs[i].Value()
				}
				return m, util.CmdHandler(CloseMultiArgumentsDialogMsg{
					Submit:    true,
					CommandID: m.commandID,
					Content:   m.content,
					Args:      args,
				})
			}
			// Move to the next input
			m.inputs[m.focusIndex].Blur()
			m.focusIndex++
			m.inputs[m.focusIndex].SetStyles(argumentInputStyles(m.inputs[m.focusIndex], t))
			return m, m.inputs[m.focusIndex].Focus()
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			if len(m.inputs) > 0 {
				m.inputs[m.focusIndex].Blur()
				m.focusIndex = (m.focusIndex + 1) % len(m.inputs)
				m.inputs[m.focusIndex].SetStyles(argumentInputStyles(m.inputs[m.focusIndex], t))
				return m, m.inputs[m.focusIndex].Focus()
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab"))):
			if len(m.inputs) > 0 {
				m.inputs[m.focusIndex].Blur()
				m.focusIndex = (m.focusIndex - 1 + len(m.inputs)) % len(m.inputs)
				m.inputs[m.focusIndex].SetStyles(argumentInputStyles(m.inputs[m.focusIndex], t))
				return m, m.inputs[m.focusIndex].Focus()
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Update the focused input
	if len(m.inputs) > 0 && m.focusIndex >= 0 && m.focusIndex < len(m.inputs) {
		var cmd tea.Cmd
		m.inputs[m.focusIndex], cmd = m.inputs[m.focusIndex].Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m MultiArgumentsDialogCmp) render() (string, *tea.Cursor) {
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	// Calculate width needed for content
	maxWidth := 60 // Width for explanation text
	if m.width > 0 {
		maxWidth = max(20, min(maxWidth, m.width-10))
	}

	title := lipgloss.NewStyle().
		Foreground(t.Primary()).
		Bold(true).
		Width(maxWidth).
		Padding(0, 1).
		Background(t.Background()).
		Render("Command Arguments")

	explanation := lipgloss.NewStyle().
		Foreground(t.Text()).
		Width(maxWidth).
		Padding(0, 1).
		Background(t.Background()).
		Render("This command requires multiple arguments. Please enter values for each:")

	// Track vertical offset inside content for hardware cursor placement.
	yOffset := lipgloss.Height(title) + lipgloss.Height(explanation)
	var cur *tea.Cursor

	// Create input fields for each argument
	inputFields := make([]string, len(m.inputs))
	for i, input := range m.inputs {
		// Highlight the label of the focused input
		labelStyle := lipgloss.NewStyle().
			Width(maxWidth).
			Padding(1, 1, 0, 1).
			Background(t.Background())

		if i == m.focusIndex {
			labelStyle = labelStyle.Foreground(t.Primary()).Bold(true)
		} else {
			labelStyle = labelStyle.Foreground(t.TextMuted())
		}

		label := labelStyle.Render(m.argNames[i] + ":")

		if i == m.focusIndex {
			if c := input.Cursor(); c != nil {
				// Outer box: border (1) + padding (1 top, 2 left).
				// Field style: Padding(0, 1) -> +1 left.
				c.X += 1 + 2 + 1
				c.Y += 1 + 1 + yOffset + lipgloss.Height(label)
				cur = c
			}
		}

		field := lipgloss.NewStyle().
			Foreground(t.Text()).
			Width(maxWidth).
			Padding(0, 1).
			Background(t.Background()).
			Render(input.View())

		inputFields[i] = lipgloss.JoinVertical(lipgloss.Left, label, field)
		yOffset += lipgloss.Height(inputFields[i])
	}

	// Join all elements vertically
	elements := []string{title, explanation}
	elements = append(elements, inputFields...)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		elements...,
	)

	dialogView := baseStyle.Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderBackground(t.Background()).
		BorderForeground(t.TextMuted()).
		Background(t.Background()).
		Width(lipgloss.Width(content) + 6).
		Render(content)

	return dialogView, cur
}

// View implements tea.Model.
func (m MultiArgumentsDialogCmp) View() tea.View {
	content, cur := m.render()
	v := tea.NewView(content)
	v.Cursor = cur
	return v
}

// Draw implements layout.Drawable.
func (m MultiArgumentsDialogCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	content, cur := m.render()
	layout.DrawCenterCursor(scr, area, content, cur)
	return cur
}

// SetSize sets the size of the component.
func (m *MultiArgumentsDialogCmp) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Bindings returns key bindings for the dialog.
func (m MultiArgumentsDialogCmp) Bindings() []key.Binding {
	return m.keys.ShortHelp()
}

// BindingKeys implements layout.Bindings.
func (m MultiArgumentsDialogCmp) BindingKeys() []key.Binding {
	return m.keys.ShortHelp()
}
