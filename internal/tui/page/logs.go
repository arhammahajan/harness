package page

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	uvlayout "github.com/charmbracelet/ultraviolet/layout"
	"github.com/arhammahajan/harness/internal/tui/components/logs"
	"github.com/arhammahajan/harness/internal/tui/layout"
)

var LogsPage PageID = "logs"

type LogPage interface {
	tea.Model
	layout.Sizeable
	layout.Bindings
	layout.Drawable
}

type logsPage struct {
	width, height int
	table         layout.Container
	details       layout.Container
}

var _ LogPage = (*logsPage)(nil)

func (p *logsPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return p, p.SetSize(msg.Width, msg.Height)
	}

	table, cmd := p.table.Update(msg)
	cmds = append(cmds, cmd)
	p.table = table.(layout.Container)
	details, cmd := p.details.Update(msg)
	cmds = append(cmds, cmd)
	p.details = details.(layout.Container)

	return p, tea.Batch(cmds...)
}

// Draw renders the logs table in the upper half and details pane in the lower
// half of area directly onto scr.
func (p *logsPage) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}
	if area.Dx() != p.width || area.Dy() != p.height {
		p.SetSize(area.Dx(), area.Dy())
	}

	if bg := themeBackground(); bg != nil {
		bgView := lipgloss.NewStyle().
			Width(area.Dx()).
			Height(area.Dy()).
			Background(bg).
			Render("")
		uv.NewStyledString(bgView).Draw(scr, area)
	}

	var tableArea, detailsArea uv.Rectangle
	uvlayout.Vertical(
		uvlayout.Len(area.Dy()/2),
		uvlayout.Fill(1),
	).Split(area).Assign(&tableArea, &detailsArea)

	if !tableArea.Empty() {
		p.table.Draw(scr, tableArea)
	}
	if !detailsArea.Empty() {
		p.details.Draw(scr, detailsArea)
	}
	return nil
}

// View renders the logs page via a temporary Ultraviolet screen buffer so View
// and Draw produce identical output.
func (p *logsPage) View() tea.View {
	buf := uv.NewScreenBuffer(max(1, p.width), max(1, p.height))
	cur := p.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (p *logsPage) BindingKeys() []key.Binding {
	return p.table.BindingKeys()
}

// GetSize implements LogPage.
func (p *logsPage) GetSize() (int, int) {
	return p.width, p.height
}

// SetSize implements LogPage.
func (p *logsPage) SetSize(width int, height int) tea.Cmd {
	p.width = width
	p.height = height
	tableHeight := height / 2
	detailsHeight := height - tableHeight
	return tea.Batch(
		p.table.SetSize(width, tableHeight),
		p.details.SetSize(width, detailsHeight),
	)
}

func (p *logsPage) Init() tea.Cmd {
	return tea.Batch(
		p.table.Init(),
		p.details.Init(),
	)
}

func NewLogsPage() LogPage {
	return &logsPage{
		table: layout.NewContainer(
			logs.NewLogsTable(),
			layout.WithBorderAll(),
			layout.WithBackgroundFunc(themeBackground),
			layout.WithBorderColorFunc(themeBorderNormal),
		),
		details: layout.NewContainer(
			logs.NewLogsDetails(),
			layout.WithBorderAll(),
			layout.WithBackgroundFunc(themeBackground),
			layout.WithBorderColorFunc(themeBorderNormal),
		),
	}
}
