package layout

import (
	"image"
	"image/color"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Container wraps a child Bubble Tea model with optional borders, padding, and
// background styling while supporting both standalone View rendering and direct
// Ultraviolet cell-buffer drawing.
type Container interface {
	tea.Model
	Sizeable
	Bindings
	Drawable
}

type container struct {
	width  int
	height int

	content tea.Model

	// Style options
	paddingTop    int
	paddingRight  int
	paddingBottom int
	paddingLeft   int

	borderTop    bool
	borderRight  bool
	borderBottom bool
	borderLeft   bool
	borderStyle  lipgloss.Border

	backgroundFunc  func() color.Color
	borderColorFunc func() color.Color
}

func (c *container) Init() tea.Cmd {
	if c.content == nil {
		return nil
	}
	return c.content.Init()
}

func (c *container) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if c.content == nil {
		return c, nil
	}
	u, cmd := c.content.Update(msg)
	c.content = u
	return c, cmd
}

func (c *container) hasBorder() bool {
	return c.borderTop || c.borderRight || c.borderBottom || c.borderLeft
}

func (c *container) background() color.Color {
	if c.backgroundFunc != nil {
		return c.backgroundFunc()
	}
	return nil
}

func (c *container) borderColor() color.Color {
	if c.borderColorFunc != nil {
		return c.borderColorFunc()
	}
	return nil
}

func (c *container) innerArea(area uv.Rectangle) uv.Rectangle {
	minX := area.Min.X + c.paddingLeft
	maxX := area.Max.X - c.paddingRight
	minY := area.Min.Y + c.paddingTop
	maxY := area.Max.Y - c.paddingBottom
	if c.borderLeft {
		minX++
	}
	if c.borderRight {
		maxX--
	}
	if c.borderTop {
		minY++
	}
	if c.borderBottom {
		maxY--
	}
	if maxX < minX {
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func (c *container) chromeStyle(width, height int) lipgloss.Style {
	style := lipgloss.NewStyle().Width(width).Height(height)
	bg := c.background()
	if bg != nil {
		style = style.Background(bg)
	}
	if c.hasBorder() {
		style = style.Border(c.borderStyle, c.borderTop, c.borderRight, c.borderBottom, c.borderLeft)
		if bg != nil {
			style = style.BorderBackground(bg)
		}
		if bc := c.borderColor(); bc != nil {
			style = style.BorderForeground(bc)
		}
	}
	return style
}

// Draw renders the container chrome (background and border) into area and
// delegates the inset inner area to the wrapped content. If content implements
// [Drawable], it draws directly into scr; otherwise its [tea.Model.View]
// content is drawn via [uv.StyledString] and any returned cursor is translated
// by the inner area's origin.
func (c *container) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	if area.Empty() {
		return nil
	}

	bg := c.background()
	if c.hasBorder() || bg != nil {
		shell := c.chromeStyle(area.Dx(), area.Dy()).Render("")
		uv.NewStyledString(shell).Draw(scr, area)
	} else {
		ClearArea(scr, area)
	}

	if c.content == nil {
		return nil
	}
	inner := c.innerArea(area)
	if inner.Empty() {
		return nil
	}

	if d, ok := c.content.(Drawable); ok {
		return d.Draw(scr, inner)
	}

	v := c.content.View()
	if v.Content != "" {
		uv.NewStyledString(v.Content).Draw(scr, inner)
	}
	if v.Cursor != nil {
		cur := *v.Cursor
		cur.X += inner.Min.X
		cur.Y += inner.Min.Y
		return &cur
	}
	return nil
}

// View renders the container as a standalone [tea.View] using a temporary
// Ultraviolet screen buffer so View and Draw produce identical output.
func (c *container) View() tea.View {
	if c.width <= 0 || c.height <= 0 {
		if c.content == nil {
			return tea.NewView("")
		}
		style := lipgloss.NewStyle().
			PaddingTop(c.paddingTop).
			PaddingRight(c.paddingRight).
			PaddingBottom(c.paddingBottom).
			PaddingLeft(c.paddingLeft)
		if bg := c.background(); bg != nil {
			style = style.Background(bg)
		}
		if c.hasBorder() {
			style = style.Border(c.borderStyle, c.borderTop, c.borderRight, c.borderBottom, c.borderLeft)
			if bg := c.background(); bg != nil {
				style = style.BorderBackground(bg)
			}
			if bc := c.borderColor(); bc != nil {
				style = style.BorderForeground(bc)
			}
		}
		v := c.content.View()
		v.SetContent(style.Render(v.Content))
		return v
	}

	buf := uv.NewScreenBuffer(c.width, c.height)
	cur := c.Draw(buf, buf.Bounds())
	v := tea.NewView(buf.Render())
	v.Cursor = cur
	return v
}

func (c *container) SetSize(width, height int) tea.Cmd {
	c.width = width
	c.height = height

	if sizeable, ok := c.content.(Sizeable); ok {
		inner := c.innerArea(image.Rect(0, 0, max(0, width), max(0, height)))
		return sizeable.SetSize(inner.Dx(), inner.Dy())
	}
	return nil
}

func (c *container) GetSize() (int, int) {
	return c.width, c.height
}

func (c *container) BindingKeys() []key.Binding {
	if b, ok := c.content.(Bindings); ok {
		return b.BindingKeys()
	}
	return []key.Binding{}
}

// IsFocused delegates focus inspection to the wrapped content if it implements
// [Focusable].
func (c *container) IsFocused() bool {
	if f, ok := c.content.(Focusable); ok {
		return f.IsFocused()
	}
	return false
}

// ContainerOption configures a [Container] constructed via [NewContainer].
type ContainerOption func(*container)

// NewContainer wraps content in a [Container] configured by options.
func NewContainer(content tea.Model, options ...ContainerOption) Container {
	c := &container{
		content:     content,
		borderStyle: lipgloss.NormalBorder(),
	}

	for _, option := range options {
		option(c)
	}

	return c
}

// WithPadding sets top, right, bottom, and left padding in cells.
func WithPadding(top, right, bottom, left int) ContainerOption {
	return func(c *container) {
		c.paddingTop = top
		c.paddingRight = right
		c.paddingBottom = bottom
		c.paddingLeft = left
	}
}

// WithPaddingAll sets uniform padding on all four sides.
func WithPaddingAll(padding int) ContainerOption {
	return WithPadding(padding, padding, padding, padding)
}

// WithPaddingHorizontal sets left and right padding.
func WithPaddingHorizontal(padding int) ContainerOption {
	return func(c *container) {
		c.paddingLeft = padding
		c.paddingRight = padding
	}
}

// WithPaddingVertical sets top and bottom padding.
func WithPaddingVertical(padding int) ContainerOption {
	return func(c *container) {
		c.paddingTop = padding
		c.paddingBottom = padding
	}
}

// WithBorder enables or disables borders on each side.
func WithBorder(top, right, bottom, left bool) ContainerOption {
	return func(c *container) {
		c.borderTop = top
		c.borderRight = right
		c.borderBottom = bottom
		c.borderLeft = left
	}
}

// WithBorderAll enables borders on all four sides.
func WithBorderAll() ContainerOption {
	return WithBorder(true, true, true, true)
}

// WithBorderHorizontal enables top and bottom borders.
func WithBorderHorizontal() ContainerOption {
	return WithBorder(true, false, true, false)
}

// WithBorderVertical enables left and right borders.
func WithBorderVertical() ContainerOption {
	return WithBorder(false, true, false, true)
}

// WithBorderStyle sets the lipgloss border glyph style.
func WithBorderStyle(style lipgloss.Border) ContainerOption {
	return func(c *container) {
		c.borderStyle = style
	}
}

// WithRoundedBorder sets a rounded border style.
func WithRoundedBorder() ContainerOption {
	return WithBorderStyle(lipgloss.RoundedBorder())
}

// WithThickBorder sets a thick border style.
func WithThickBorder() ContainerOption {
	return WithBorderStyle(lipgloss.ThickBorder())
}

// WithDoubleBorder sets a double border style.
func WithDoubleBorder() ContainerOption {
	return WithBorderStyle(lipgloss.DoubleBorder())
}

// WithBackground sets a static background color for the container chrome.
func WithBackground(col color.Color) ContainerOption {
	return func(c *container) {
		c.backgroundFunc = func() color.Color { return col }
	}
}

// WithBackgroundFunc sets a dynamic background color provider evaluated on each
// draw (useful for live theme switching without coupling layout to theme).
func WithBackgroundFunc(fn func() color.Color) ContainerOption {
	return func(c *container) {
		c.backgroundFunc = fn
	}
}

// WithBorderColor sets a static border foreground color.
func WithBorderColor(col color.Color) ContainerOption {
	return func(c *container) {
		c.borderColorFunc = func() color.Color { return col }
	}
}

// WithBorderColorFunc sets a dynamic border foreground color provider evaluated
// on each draw.
func WithBorderColorFunc(fn func() color.Color) ContainerOption {
	return func(c *container) {
		c.borderColorFunc = fn
	}
}
