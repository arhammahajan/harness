package logs

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
)

type DetailComponent interface {
	tea.Model
	layout.Sizeable
	layout.Bindings
	layout.Drawable
}

type detailCmp struct {
	width, height int
	currentLog    logging.LogMessage
	viewport      viewport.Model
	drawCache     *render.DrawCache
}

func (i *detailCmp) Init() tea.Cmd {
	messages := logging.List()
	if len(messages) == 0 {
		return nil
	}
	i.currentLog = messages[0]
	i.updateContent()
	return nil
}

func (i *detailCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case selectedLogMsg:
		if msg.ID != i.currentLog.ID {
			i.currentLog = logging.LogMessage(msg)
			i.updateContent()
			i.viewport.GotoTop()
		}
		return i, nil
	case render.CoalescedWheelMsg:
		i.HandleWheel(msg.DeltaX, msg.DeltaY)
		return i, nil
	case tea.KeyPressMsg, tea.MouseWheelMsg:
		var cmd tea.Cmd
		i.viewport, cmd = i.viewport.Update(msg)
		return i, cmd
	}

	return i, nil
}

// HandleWheel implements [render.WheelScrollable] for coalesced mouse wheel scrolling.
func (i *detailCmp) HandleWheel(deltaX, deltaY float64) {
	step := i.viewport.MouseWheelDelta
	if step <= 0 {
		step = 3
	}
	if deltaY < 0 {
		i.viewport.ScrollUp(max(1, int(-deltaY*float64(step))))
	} else if deltaY > 0 {
		i.viewport.ScrollDown(max(1, int(deltaY*float64(step))))
	}
	if deltaX < 0 {
		i.viewport.ScrollLeft(max(1, int(-deltaX*float64(step))))
	} else if deltaX > 0 {
		i.viewport.ScrollRight(max(1, int(deltaX*float64(step))))
	}
}

func (i *detailCmp) updateContent() {
	var content strings.Builder
	t := theme.CurrentTheme()
	bg := t.Background()

	// Format the header with timestamp and level
	timeStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Background(bg)
	levelStyle := getLevelStyle(i.currentLog.Level).Background(bg)
	sepStyle := lipgloss.NewStyle().Background(bg)

	header := lipgloss.JoinHorizontal(
		lipgloss.Center,
		timeStyle.Render(i.currentLog.Time.Format(time.RFC3339)),
		sepStyle.Render("  "),
		levelStyle.Render(i.currentLog.Level),
	)

	content.WriteString(lipgloss.NewStyle().Bold(true).Background(bg).Render(header))
	content.WriteString("\n\n")

	// Message with styling
	messageStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Text()).Background(bg)
	content.WriteString(messageStyle.Render("Message:"))
	content.WriteString("\n")
	content.WriteString(lipgloss.NewStyle().Foreground(t.Text()).Background(bg).Padding(0, 2).Render(i.currentLog.Message))
	content.WriteString("\n\n")

	// Attributes section
	if len(i.currentLog.Attributes) > 0 {
		attrHeaderStyle := lipgloss.NewStyle().Bold(true).Foreground(t.Text()).Background(bg)
		content.WriteString(attrHeaderStyle.Render("Attributes:"))
		content.WriteString("\n")

		// Create a table-like display for attributes
		keyStyle := lipgloss.NewStyle().Foreground(t.Primary()).Background(bg).Bold(true)
		valueStyle := lipgloss.NewStyle().Foreground(t.Text()).Background(bg)
		colonStyle := lipgloss.NewStyle().Foreground(t.Text()).Background(bg)

		for _, attr := range i.currentLog.Attributes {
			attrLine := fmt.Sprintf("%s%s%s",
				keyStyle.Render(attr.Key),
				colonStyle.Render(": "),
				valueStyle.Render(attr.Value),
			)
			content.WriteString(lipgloss.NewStyle().Background(bg).Padding(0, 2).Render(attrLine))
			content.WriteString("\n")
		}
	}

	i.viewport.SetContent(content.String())
}

func getLevelStyle(level string) lipgloss.Style {
	style := lipgloss.NewStyle().Bold(true)
	t := theme.CurrentTheme()

	switch strings.ToLower(level) {
	case "info":
		return style.Foreground(t.Info())
	case "warn", "warning":
		return style.Foreground(t.Warning())
	case "error", "err":
		return style.Foreground(t.Error())
	case "debug":
		return style.Foreground(t.Success())
	default:
		return style.Foreground(t.Text())
	}
}

func (i *detailCmp) renderView() string {
	t := theme.CurrentTheme()
	i.viewport.Style = lipgloss.NewStyle().
		Background(t.Background()).
		Foreground(t.Text())

	style := lipgloss.NewStyle().Background(t.Background())
	if i.width > 0 && i.height > 0 {
		style = style.Width(i.width).Height(i.height)
	}
	return style.Render(i.viewport.View())
}

func (i *detailCmp) View() tea.View {
	return tea.NewView(i.renderView())
}

func (i *detailCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}
	if i.drawCache == nil {
		i.drawCache = render.NewDrawCache()
	}
	i.drawCache.Draw(scr, area, i.renderView())
	return nil
}

func (i *detailCmp) GetSize() (int, int) {
	return i.width, i.height
}

func (i *detailCmp) SetSize(width int, height int) tea.Cmd {
	i.width = width
	i.height = height
	i.viewport.SetWidth(i.width)
	i.viewport.SetHeight(i.height)
	i.updateContent()
	return nil
}

func (i *detailCmp) BindingKeys() []key.Binding {
	return layout.KeyMapToSlice(i.viewport.KeyMap)
}

func NewLogsDetails() DetailComponent {
	return &detailCmp{
		viewport:  viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
		drawCache: render.NewDrawCache(),
	}
}
