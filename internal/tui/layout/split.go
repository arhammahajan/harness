package layout

import (
	"image"
	"image/color"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	uvlayout "github.com/charmbracelet/ultraviolet/layout"
)

// SplitPaneLayout partitions an area into optional left, right, and bottom
// panels using Ultraviolet constraint layout rectangles.
type SplitPaneLayout interface {
	tea.Model
	Sizeable
	Bindings
	Drawable
	SetLeftPanel(panel Container) tea.Cmd
	SetRightPanel(panel Container) tea.Cmd
	SetBottomPanel(panel Container) tea.Cmd

	ClearLeftPanel() tea.Cmd
	ClearRightPanel() tea.Cmd
	ClearBottomPanel() tea.Cmd
}

type splitPaneLayout struct {
	width         int
	height        int
	ratio         float64
	verticalRatio float64

	rightPanel  Container
	leftPanel   Container
	bottomPanel Container

	backgroundFunc func() color.Color
}

// SplitPaneOption configures a [SplitPaneLayout] created with [NewSplitPane].
type SplitPaneOption func(*splitPaneLayout)

func (s *splitPaneLayout) Init() tea.Cmd {
	var cmds []tea.Cmd

	if s.leftPanel != nil {
		cmds = append(cmds, s.leftPanel.Init())
	}

	if s.rightPanel != nil {
		cmds = append(cmds, s.rightPanel.Init())
	}

	if s.bottomPanel != nil {
		cmds = append(cmds, s.bottomPanel.Init())
	}

	return tea.Batch(cmds...)
}

func (s *splitPaneLayout) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return s, s.SetSize(msg.Width, msg.Height)
	}

	if s.rightPanel != nil {
		u, cmd := s.rightPanel.Update(msg)
		s.rightPanel = u.(Container)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if s.leftPanel != nil {
		u, cmd := s.leftPanel.Update(msg)
		s.leftPanel = u.(Container)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if s.bottomPanel != nil {
		u, cmd := s.bottomPanel.Update(msg)
		s.bottomPanel = u.(Container)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return s, tea.Batch(cmds...)
}

// computeAreas resolves exact non-overlapping [uv.Rectangle] regions for the
// left, right, and bottom panels using Ultraviolet's constraint layout solver.
func (s *splitPaneLayout) computeAreas(area uv.Rectangle) (leftArea, rightArea, bottomArea uv.Rectangle) {
	if area.Empty() {
		return
	}

	hasTop := s.leftPanel != nil || s.rightPanel != nil
	var topArea uv.Rectangle

	if s.bottomPanel != nil && hasTop {
		topHeight := int(float64(area.Dy()) * s.verticalRatio)
		uvlayout.Vertical(
			uvlayout.Len(topHeight),
			uvlayout.Fill(1),
		).Split(area).Assign(&topArea, &bottomArea)
	} else if s.bottomPanel != nil {
		bottomArea = area
		return
	} else {
		topArea = area
	}

	if s.leftPanel != nil && s.rightPanel != nil {
		leftWidth := int(float64(topArea.Dx()) * s.ratio)
		uvlayout.Horizontal(
			uvlayout.Len(leftWidth),
			uvlayout.Fill(1),
		).Split(topArea).Assign(&leftArea, &rightArea)
	} else if s.leftPanel != nil {
		leftArea = topArea
	} else if s.rightPanel != nil {
		rightArea = topArea
	}

	return leftArea, rightArea, bottomArea
}

// Draw renders all configured panels directly into their computed Ultraviolet
// rectangles on scr and returns the active panel's cursor (preferring a focused
// panel if one reports a cursor).
func (s *splitPaneLayout) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	if area.Dx() != s.width || area.Dy() != s.height {
		s.SetSize(area.Dx(), area.Dy())
	}

	if s.backgroundFunc != nil {
		if bg := s.backgroundFunc(); bg != nil {
			bgView := lipgloss.NewStyle().
				Width(area.Dx()).
				Height(area.Dy()).
				Background(bg).
				Render("")
			uv.NewStyledString(bgView).Draw(scr, area)
		}
	}

	leftArea, rightArea, bottomArea := s.computeAreas(area)

	var (
		cursor        *tea.Cursor
		focusedCursor *tea.Cursor
	)
	drawPanel := func(panel Container, rect uv.Rectangle) {
		if panel == nil || rect.Empty() {
			return
		}
		cur := panel.Draw(scr, rect)
		if cur == nil {
			return
		}
		if f, ok := panel.(interface{ IsFocused() bool }); ok && f.IsFocused() {
			focusedCursor = cur
		} else if cursor == nil {
			cursor = cur
		}
	}

	drawPanel(s.leftPanel, leftArea)
	drawPanel(s.rightPanel, rightArea)
	drawPanel(s.bottomPanel, bottomArea)

	if focusedCursor != nil {
		return focusedCursor
	}
	return cursor
}

// View renders the split pane layout into a temporary Ultraviolet screen buffer
// and returns the resulting [tea.View].
func (s *splitPaneLayout) View() tea.View {
	if s.width <= 0 || s.height <= 0 {
		return tea.NewView("")
	}
	buf := uv.NewScreenBuffer(s.width, s.height)
	cur := s.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (s *splitPaneLayout) SetSize(width, height int) tea.Cmd {
	s.width = width
	s.height = height

	leftArea, rightArea, bottomArea := s.computeAreas(image.Rect(0, 0, max(0, width), max(0, height)))

	var cmds []tea.Cmd
	if s.leftPanel != nil {
		if cmd := s.leftPanel.SetSize(leftArea.Dx(), leftArea.Dy()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if s.rightPanel != nil {
		if cmd := s.rightPanel.SetSize(rightArea.Dx(), rightArea.Dy()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	if s.bottomPanel != nil {
		if cmd := s.bottomPanel.SetSize(bottomArea.Dx(), bottomArea.Dy()); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (s *splitPaneLayout) GetSize() (int, int) {
	return s.width, s.height
}

func (s *splitPaneLayout) SetLeftPanel(panel Container) tea.Cmd {
	s.leftPanel = panel
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) SetRightPanel(panel Container) tea.Cmd {
	s.rightPanel = panel
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) SetBottomPanel(panel Container) tea.Cmd {
	s.bottomPanel = panel
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) ClearLeftPanel() tea.Cmd {
	s.leftPanel = nil
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) ClearRightPanel() tea.Cmd {
	s.rightPanel = nil
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) ClearBottomPanel() tea.Cmd {
	s.bottomPanel = nil
	if s.width > 0 && s.height > 0 {
		return s.SetSize(s.width, s.height)
	}
	return nil
}

func (s *splitPaneLayout) BindingKeys() []key.Binding {
	keys := []key.Binding{}
	if s.leftPanel != nil {
		keys = append(keys, s.leftPanel.BindingKeys()...)
	}
	if s.rightPanel != nil {
		keys = append(keys, s.rightPanel.BindingKeys()...)
	}
	if s.bottomPanel != nil {
		keys = append(keys, s.bottomPanel.BindingKeys()...)
	}
	return keys
}

// NewSplitPane constructs a [SplitPaneLayout] with default 70/30 horizontal and
// 90/10 vertical split ratios unless overridden by options.
func NewSplitPane(options ...SplitPaneOption) SplitPaneLayout {
	l := &splitPaneLayout{
		ratio:         0.7,
		verticalRatio: 0.9,
	}
	for _, option := range options {
		option(l)
	}
	return l
}

// WithLeftPanel sets the initial left panel.
func WithLeftPanel(panel Container) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.leftPanel = panel
	}
}

// WithRightPanel sets the initial right panel.
func WithRightPanel(panel Container) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.rightPanel = panel
	}
}

// WithRatio sets the horizontal split ratio allocated to the left panel.
func WithRatio(ratio float64) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.ratio = ratio
	}
}

// WithBottomPanel sets the initial bottom panel.
func WithBottomPanel(panel Container) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.bottomPanel = panel
	}
}

// WithVerticalRatio sets the vertical split ratio allocated to the top section.
func WithVerticalRatio(ratio float64) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.verticalRatio = ratio
	}
}

// WithSplitBackground sets a static background color behind the split panels.
func WithSplitBackground(col color.Color) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.backgroundFunc = func() color.Color { return col }
	}
}

// WithSplitBackgroundFunc sets a dynamic background color provider evaluated on
// each draw.
func WithSplitBackgroundFunc(fn func() color.Color) SplitPaneOption {
	return func(s *splitPaneLayout) {
		s.backgroundFunc = fn
	}
}
