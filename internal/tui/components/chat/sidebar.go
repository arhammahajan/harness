package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/diff"
	"github.com/arhammahajan/harness/internal/history"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/dialog"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
)

type sidebarCmp struct {
	width, height int
	session       session.Session
	history       history.Service
	modFiles      map[string]struct {
		additions int
		removals  int
	}
	drawCache *render.DrawCache
}

var _ layout.Drawable = (*sidebarCmp)(nil)

func (m *sidebarCmp) Init() tea.Cmd {
	if m.history != nil {
		ctx := context.Background()
		filesCh := m.history.Subscribe(ctx)

		m.modFiles = make(map[string]struct {
			additions int
			removals  int
		})

		m.loadModifiedFiles(ctx)

		return func() tea.Msg {
			return <-filesCh
		}
	}
	return nil
}

func (m *sidebarCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case dialog.ThemeChangedMsg:
		m.drawCache.Invalidate()
	case SessionSelectedMsg:
		if msg.ID != m.session.ID {
			m.session = msg
			ctx := context.Background()
			m.loadModifiedFiles(ctx)
		}
	case pubsub.Event[session.Session]:
		if msg.Type == pubsub.UpdatedEvent {
			if m.session.ID == msg.Payload.ID {
				m.session = msg.Payload
			}
		}
	case pubsub.Event[history.File]:
		if msg.Payload.SessionID == m.session.ID {
			ctx := context.Background()
			m.processFileChanges(ctx, msg.Payload)

			return m, func() tea.Msg {
				ctx := context.Background()
				filesCh := m.history.Subscribe(ctx)
				return <-filesCh
			}
		}
	}
	return m, nil
}

func (m *sidebarCmp) renderContent(width, height int) string {
	width = max(1, width)
	height = max(1, height)

	padLeft, padRight := 4, 2
	if width <= padLeft+padRight {
		padLeft, padRight = 0, 0
	}
	innerWidth := max(1, width-padLeft-padRight)

	baseStyle := styles.BaseStyle()

	return baseStyle.
		Width(width).
		Height(height).
		PaddingLeft(padLeft).
		PaddingRight(padRight).
		Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				header(innerWidth),
				" ",
				m.sessionSection(innerWidth),
				" ",
				lspsConfigured(innerWidth),
				" ",
				m.modifiedFiles(innerWidth),
			),
		)
}

func (m *sidebarCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}
	content := m.renderContent(area.Dx(), area.Dy())
	m.drawCache.Draw(scr, area, content)
	return nil
}

func (m *sidebarCmp) View() tea.View {
	w := max(1, m.width)
	h := max(1, m.height)
	buf := uv.NewScreenBuffer(w, h)
	cur := m.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (m *sidebarCmp) sessionSection(width int) string {
	width = max(1, width)
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	sessionKey := baseStyle.
		Foreground(t.Primary()).
		Bold(true).
		Render("Session")

	valWidth := max(1, width-lipgloss.Width(sessionKey))
	sessionValue := baseStyle.
		Foreground(t.Text()).
		Width(valWidth).
		Render(fmt.Sprintf(": %s", m.session.Title))

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		sessionKey,
		sessionValue,
	)
}

func (m *sidebarCmp) modifiedFile(width int, filePath string, additions, removals int) string {
	width = max(1, width)
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	stats := ""
	if additions > 0 && removals > 0 {
		additionsStr := baseStyle.
			Foreground(t.Success()).
			PaddingLeft(1).
			Render(fmt.Sprintf("+%d", additions))

		removalsStr := baseStyle.
			Foreground(t.Error()).
			PaddingLeft(1).
			Render(fmt.Sprintf("-%d", removals))

		content := lipgloss.JoinHorizontal(lipgloss.Top, additionsStr, removalsStr)
		stats = baseStyle.Width(lipgloss.Width(content)).Render(content)
	} else if additions > 0 {
		additionsStr := fmt.Sprintf(" %s", baseStyle.
			PaddingLeft(1).
			Foreground(t.Success()).
			Render(fmt.Sprintf("+%d", additions)))
		stats = baseStyle.Width(lipgloss.Width(additionsStr)).Render(additionsStr)
	} else if removals > 0 {
		removalsStr := fmt.Sprintf(" %s", baseStyle.
			PaddingLeft(1).
			Foreground(t.Error()).
			Render(fmt.Sprintf("-%d", removals)))
		stats = baseStyle.Width(lipgloss.Width(removalsStr)).Render(removalsStr)
	}

	filePathStr := baseStyle.Render(filePath)

	return baseStyle.
		Width(width).
		Render(
			lipgloss.JoinHorizontal(
				lipgloss.Top,
				filePathStr,
				stats,
			),
		)
}

func (m *sidebarCmp) modifiedFiles(width int) string {
	width = max(1, width)
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	modifiedFiles := baseStyle.
		Width(width).
		Foreground(t.Primary()).
		Bold(true).
		Render("Modified Files:")

	if len(m.modFiles) == 0 {
		msg := "No modified files"
		remainingWidth := width - lipgloss.Width(msg)
		if remainingWidth > 0 {
			msg += strings.Repeat(" ", remainingWidth)
		}
		return baseStyle.
			Width(width).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Left,
					modifiedFiles,
					baseStyle.Foreground(t.TextMuted()).Render(msg),
				),
			)
	}

	var paths []string
	for path := range m.modFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var fileViews []string
	for _, path := range paths {
		stats := m.modFiles[path]
		fileViews = append(fileViews, m.modifiedFile(width, path, stats.additions, stats.removals))
	}

	return baseStyle.
		Width(width).
		Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				modifiedFiles,
				lipgloss.JoinVertical(
					lipgloss.Left,
					fileViews...,
				),
			),
		)
}

func (m *sidebarCmp) SetSize(width, height int) tea.Cmd {
	m.width = width
	m.height = height
	return nil
}

func (m *sidebarCmp) GetSize() (int, int) {
	return m.width, m.height
}

func NewSidebarCmp(session session.Session, history history.Service) tea.Model {
	return &sidebarCmp{
		session:   session,
		history:   history,
		drawCache: render.NewDrawCache(),
	}
}

func (m *sidebarCmp) loadModifiedFiles(ctx context.Context) {
	if m.history == nil || m.session.ID == "" {
		return
	}

	latestFiles, err := m.history.ListLatestSessionFiles(ctx, m.session.ID)
	if err != nil {
		return
	}

	allFiles, err := m.history.ListBySession(ctx, m.session.ID)
	if err != nil {
		return
	}

	m.modFiles = make(map[string]struct {
		additions int
		removals  int
	})

	for _, file := range latestFiles {
		if file.Version == history.InitialVersion {
			continue
		}

		var initialVersion history.File
		for _, v := range allFiles {
			if v.Path == file.Path && v.Version == history.InitialVersion {
				initialVersion = v
				break
			}
		}

		if initialVersion.ID == "" {
			continue
		}
		if initialVersion.Content == file.Content {
			continue
		}

		_, additions, removals := diff.GenerateDiff(initialVersion.Content, file.Content, file.Path)

		if additions > 0 || removals > 0 {
			displayPath := getDisplayPath(file.Path)
			m.modFiles[displayPath] = struct {
				additions int
				removals  int
			}{
				additions: additions,
				removals:  removals,
			}
		}
	}
}

func (m *sidebarCmp) processFileChanges(ctx context.Context, file history.File) {
	if file.Version == history.InitialVersion {
		return
	}

	initialVersion, err := m.findInitialVersion(ctx, file.Path)
	if err != nil || initialVersion.ID == "" {
		return
	}

	if initialVersion.Content == file.Content {
		displayPath := getDisplayPath(file.Path)
		delete(m.modFiles, displayPath)
		return
	}

	_, additions, removals := diff.GenerateDiff(initialVersion.Content, file.Content, file.Path)

	if additions > 0 || removals > 0 {
		displayPath := getDisplayPath(file.Path)
		if m.modFiles == nil {
			m.modFiles = make(map[string]struct {
				additions int
				removals  int
			})
		}
		m.modFiles[displayPath] = struct {
			additions int
			removals  int
		}{
			additions: additions,
			removals:  removals,
		}
	} else {
		displayPath := getDisplayPath(file.Path)
		delete(m.modFiles, displayPath)
	}
}

func (m *sidebarCmp) findInitialVersion(ctx context.Context, path string) (history.File, error) {
	fileVersions, err := m.history.ListBySession(ctx, m.session.ID)
	if err != nil {
		return history.File{}, err
	}

	for _, v := range fileVersions {
		if v.Path == path && v.Version == history.InitialVersion {
			return v, nil
		}
	}

	return history.File{}, fmt.Errorf("initial version not found")
}

func getDisplayPath(path string) string {
	wd := workingDirectory()
	displayPath := path
	if wd != "" {
		displayPath = strings.TrimPrefix(displayPath, wd)
	}
	return strings.TrimPrefix(displayPath, "/")
}
