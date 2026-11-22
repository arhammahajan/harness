package logs

import (
	"encoding/json"
	"slices"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

type TableComponent interface {
	tea.Model
	layout.Sizeable
	layout.Bindings
	layout.Drawable
}

type tableCmp struct {
	width, height int
	table         table.Model
	drawCache     *render.DrawCache
}

type selectedLogMsg logging.LogMessage

func (i *tableCmp) Init() tea.Cmd {
	i.setRows()
	return nil
}

func (i *tableCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg.(type) {
	case pubsub.Event[logging.LogMessage]:
		i.setRows()
		return i, nil
	}
	prevSelectedRow := i.table.SelectedRow()
	t, cmd := i.table.Update(msg)
	cmds = append(cmds, cmd)
	i.table = t
	selectedRow := i.table.SelectedRow()
	if len(selectedRow) > 0 {
		if len(prevSelectedRow) == 0 || selectedRow[0] != prevSelectedRow[0] {
			var log logging.LogMessage
			for _, row := range logging.List() {
				if row.ID == selectedRow[0] {
					log = row
					break
				}
			}
			if log.ID != "" {
				cmds = append(cmds, util.CmdHandler(selectedLogMsg(log)))
			}
		}
	}
	return i, tea.Batch(cmds...)
}

func (i *tableCmp) renderView() string {
	t := theme.CurrentTheme()
	tableStyles := table.DefaultStyles()
	tableStyles.Header = tableStyles.Header.
		Foreground(t.TextEmphasized()).
		Background(t.Background())
	tableStyles.Cell = tableStyles.Cell.
		Foreground(t.Text()).
		Background(t.Background())
	tableStyles.Selected = tableStyles.Selected.
		Foreground(t.Primary()).
		Background(t.Background())
	i.table.SetStyles(tableStyles)

	style := lipgloss.NewStyle().Background(t.Background())
	if i.width > 0 && i.height > 0 {
		style = style.Width(i.width).Height(i.height)
	}
	return style.Render(i.table.View())
}

func (i *tableCmp) View() tea.View {
	return tea.NewView(i.renderView())
}

func (i *tableCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}
	if i.drawCache == nil {
		i.drawCache = render.NewDrawCache()
	}
	i.drawCache.Draw(scr, area, i.renderView())
	return nil
}

func (i *tableCmp) GetSize() (int, int) {
	return i.width, i.height
}

func (i *tableCmp) SetSize(width int, height int) tea.Cmd {
	i.width = width
	i.height = height
	i.table.SetWidth(width)
	i.table.SetHeight(height)
	columns := i.table.Columns()
	if len(columns) > 0 {
		colWidth := max(0, (width/len(columns))-2)
		for idx, col := range columns {
			col.Width = colWidth
			columns[idx] = col
		}
		i.table.SetColumns(columns)
	}
	return nil
}

func (i *tableCmp) BindingKeys() []key.Binding {
	return layout.KeyMapToSlice(i.table.KeyMap)
}

func (i *tableCmp) setRows() {
	rows := []table.Row{}

	logs := slices.Clone(logging.List())
	slices.SortFunc(logs, func(a, b logging.LogMessage) int {
		if a.Time.Before(b.Time) {
			return 1
		}
		if a.Time.After(b.Time) {
			return -1
		}
		return 0
	})

	for _, log := range logs {
		bm, _ := json.Marshal(log.Attributes)

		row := table.Row{
			log.ID,
			log.Time.Format("15:04:05"),
			log.Level,
			log.Message,
			string(bm),
		}
		rows = append(rows, row)
	}
	i.table.SetRows(rows)
}

func NewLogsTable() TableComponent {
	columns := []table.Column{
		{Title: "ID", Width: 4},
		{Title: "Time", Width: 4},
		{Title: "Level", Width: 10},
		{Title: "Message", Width: 10},
		{Title: "Attributes", Width: 10},
	}

	tableModel := table.New(
		table.WithColumns(columns),
	)
	tableModel.Focus()
	return &tableCmp{
		table:     tableModel,
		drawCache: render.NewDrawCache(),
	}
}
