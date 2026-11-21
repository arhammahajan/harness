package layout

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCenterRectAndBottomLeftRect(t *testing.T) {
	area := image.Rect(10, 5, 50, 25) // 40x20

	center := CenterRect(area, 10, 4)
	assert.Equal(t, image.Rect(25, 13, 35, 17), center)

	bl := BottomLeftRect(area, 12, 3)
	assert.Equal(t, image.Rect(10, 22, 22, 25), bl)
}

func TestClearArea(t *testing.T) {
	scr := uv.NewScreenBuffer(10, 4)
	DrawAt(scr, scr.Bounds(), "aaaaaaaaaa\nbbbbbbbbbb\ncccccccccc\ndddddddddd")

	ClearArea(scr, image.Rect(2, 1, 5, 3))
	assert.Equal(t, "a", scr.CellAt(2, 0).Content)
	assert.Equal(t, "b", scr.CellAt(1, 1).Content)
	assert.Equal(t, " ", scr.CellAt(2, 1).Content)
	assert.Equal(t, " ", scr.CellAt(4, 2).Content)
	assert.Equal(t, "b", scr.CellAt(5, 1).Content)
	assert.Equal(t, "d", scr.CellAt(2, 3).Content)
}

func TestDrawCenterCursorTranslationAndClamping(t *testing.T) {
	scr := uv.NewScreenBuffer(20, 10)
	area := image.Rect(2, 1, 18, 9) // 16x8, center origin at (2+8-3, 1+4-1) = (7, 4)

	cur := tea.NewCursor(2, 1)
	DrawCenterCursor(scr, area, "hello!\nworld!", cur)

	// 6x2 view centered inside [2,1)-(18,9) -> Min=(7, 4)
	assert.Equal(t, 9, cur.X)
	assert.Equal(t, 5, cur.Y)
	assert.Equal(t, "h", scr.CellAt(7, 4).Content)
	assert.Equal(t, "w", scr.CellAt(7, 5).Content)

	// Oversized view is clamped to area bounds.
	small := image.Rect(0, 0, 4, 2)
	cur2 := tea.NewCursor(1, 0)
	DrawCenterCursor(scr, small, "abcdefghij\n0123456789\nextra", cur2)
	assert.Equal(t, 1, cur2.X)
	assert.Equal(t, 0, cur2.Y)
	assert.Equal(t, "a", scr.CellAt(0, 0).Content)
	assert.Equal(t, "d", scr.CellAt(3, 0).Content)
	assert.Equal(t, "0", scr.CellAt(0, 1).Content)
}

func TestScrollbar(t *testing.T) {
	plain := lipgloss.NewStyle()

	// Content fits -> empty string.
	assert.Equal(t, "", Scrollbar(10, 10, 10, 0, plain, plain))
	assert.Equal(t, "", Scrollbar(10, 5, 10, 0, plain, plain))
	assert.Equal(t, "", Scrollbar(0, 100, 10, 0, plain, plain))

	// Top of scrollable region (height=4, content=20, viewport=10 -> thumbSize=2).
	top := strings.Split(Scrollbar(4, 20, 10, 0, plain, plain), "\n")
	require.Len(t, top, 4)
	assert.Equal(t, []string{ScrollbarThumb, ScrollbarThumb, ScrollbarTrack, ScrollbarTrack}, top)

	// Middle offset (maxOffset=10, offset=5 -> thumbPos=1).
	mid := strings.Split(Scrollbar(4, 20, 10, 5, plain, plain), "\n")
	assert.Equal(t, []string{ScrollbarTrack, ScrollbarThumb, ScrollbarThumb, ScrollbarTrack}, mid)

	// Bottom offset (offset=10 -> thumbPos=2).
	bot := strings.Split(Scrollbar(4, 20, 10, 10, plain, plain), "\n")
	assert.Equal(t, []string{ScrollbarTrack, ScrollbarTrack, ScrollbarThumb, ScrollbarThumb}, bot)

	// Very large content still gets minimum 1-cell thumb.
	tiny := strings.Split(Scrollbar(5, 1000, 10, 990, plain, plain), "\n")
	assert.Equal(t, []string{ScrollbarTrack, ScrollbarTrack, ScrollbarTrack, ScrollbarTrack, ScrollbarThumb}, tiny)
}

func TestKeyMapToSlice(t *testing.T) {
	type sampleMap struct {
		Up    key.Binding
		Down  key.Binding
		extra string
	}
	km := sampleMap{
		Up:    key.NewBinding(key.WithKeys("k")),
		Down:  key.NewBinding(key.WithKeys("j")),
		extra: "ignored",
	}
	got := KeyMapToSlice(km)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"k"}, got[0].Keys())
	assert.Equal(t, []string{"j"}, got[1].Keys())
	assert.Nil(t, KeyMapToSlice(nil))
	assert.Nil(t, KeyMapToSlice("not-a-struct"))
}

// mockDrawableModel implements tea.Model, Drawable, Sizeable, Focusable, Bindings.
type mockDrawableModel struct {
	label    string
	width    int
	height   int
	lastArea uv.Rectangle
	focused  bool
	cursor   *tea.Cursor
}

func (m *mockDrawableModel) Init() tea.Cmd                           { return nil }
func (m *mockDrawableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m *mockDrawableModel) View() tea.View                          { return tea.NewView(m.label) }
func (m *mockDrawableModel) SetSize(w, h int) tea.Cmd {
	m.width, m.height = w, h
	return nil
}
func (m *mockDrawableModel) GetSize() (int, int) { return m.width, m.height }
func (m *mockDrawableModel) Focus() tea.Cmd      { m.focused = true; return nil }
func (m *mockDrawableModel) Blur() tea.Cmd       { m.focused = false; return nil }
func (m *mockDrawableModel) IsFocused() bool     { return m.focused }
func (m *mockDrawableModel) BindingKeys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys(m.label))}
}

func (m *mockDrawableModel) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	m.lastArea = area
	DrawAt(scr, area, m.label)
	if m.cursor != nil {
		cur := *m.cursor
		cur.X += area.Min.X
		cur.Y += area.Min.Y
		return &cur
	}
	return nil
}

// mockViewOnlyModel implements only tea.Model (not Drawable) to test fallback path.
type mockViewOnlyModel struct {
	text   string
	cursor *tea.Cursor
}

func (m *mockViewOnlyModel) Init() tea.Cmd                           { return nil }
func (m *mockViewOnlyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m *mockViewOnlyModel) View() tea.View {
	v := tea.NewView(m.text)
	v.Cursor = m.cursor
	return v
}

func TestContainerDrawWithDrawableChild(t *testing.T) {
	child := &mockDrawableModel{
		label:  "HELLO",
		cursor: tea.NewCursor(2, 0),
	}

	borderCol := color.RGBA{R: 100, G: 150, B: 200, A: 255}
	bgCol := color.RGBA{R: 20, G: 20, B: 30, A: 255}

	c := NewContainer(
		child,
		WithBorderAll(),
		WithRoundedBorder(),
		WithPadding(1, 2, 1, 2),
		WithBackgroundFunc(func() color.Color { return bgCol }),
		WithBorderColorFunc(func() color.Color { return borderCol }),
	)

	c.SetSize(14, 7)
	// Inner dimensions: width = 14 - 2 (border) - 4 (padding) = 8; height = 7 - 2 - 2 = 3.
	w, h := child.GetSize()
	assert.Equal(t, 8, w)
	assert.Equal(t, 3, h)

	scr := uv.NewScreenBuffer(20, 12)
	drawArea := image.Rect(3, 2, 17, 9) // 14x7 at offset (3, 2)
	cur := c.Draw(scr, drawArea)

	// Child received exact inset rectangle: Min=(3+1+2, 2+1+1)=(6, 4), Max=(17-1-2, 9-1-1)=(14, 7)
	assert.Equal(t, image.Rect(6, 4, 14, 7), child.lastArea)

	// Outer border corners drawn at drawArea edges.
	assert.Equal(t, "╭", scr.CellAt(3, 2).Content)
	assert.Equal(t, "╮", scr.CellAt(16, 2).Content)
	assert.Equal(t, "╰", scr.CellAt(3, 8).Content)
	assert.Equal(t, "╯", scr.CellAt(16, 8).Content)

	// Child content drawn inside inner area.
	assert.Equal(t, "H", scr.CellAt(6, 4).Content)
	assert.Equal(t, "O", scr.CellAt(10, 4).Content)

	// Cursor returned from Drawable child is in screen coordinates.
	require.NotNil(t, cur)
	assert.Equal(t, 8, cur.X)
	assert.Equal(t, 4, cur.Y)

	// Standalone View() renders consistently and propagates cursor.
	v := c.View()
	assert.Contains(t, v.Content, "HELLO")
	require.NotNil(t, v.Cursor)
	assert.Equal(t, 5, v.Cursor.X) // 1 (border) + 2 (padding) + 2 (child cursor)
	assert.Equal(t, 2, v.Cursor.Y) // 1 (border) + 1 (padding) + 0
}

func TestContainerDrawWithNonDrawableChild(t *testing.T) {
	child := &mockViewOnlyModel{
		text:   "plain",
		cursor: tea.NewCursor(1, 0),
	}
	c := NewContainer(child, WithBorderAll(), WithPaddingAll(1))
	scr := uv.NewScreenBuffer(12, 6)

	cur := c.Draw(scr, image.Rect(1, 1, 11, 6)) // 10x5 -> inner Min=(3,3)
	assert.Equal(t, "p", scr.CellAt(3, 3).Content)
	assert.Equal(t, "n", scr.CellAt(7, 3).Content)
	require.NotNil(t, cur)
	assert.Equal(t, 4, cur.X)
	assert.Equal(t, 3, cur.Y)
}

func TestSplitPaneLayoutDrawWithUVRectangles(t *testing.T) {
	leftChild := &mockDrawableModel{label: "LEFT", cursor: tea.NewCursor(1, 0)}
	rightChild := &mockDrawableModel{label: "RIGHT", focused: true, cursor: tea.NewCursor(2, 1)}
	bottomChild := &mockDrawableModel{label: "BOTTOM"}

	sp := NewSplitPane(
		WithLeftPanel(NewContainer(leftChild)),
		WithRightPanel(NewContainer(rightChild)),
		WithBottomPanel(NewContainer(bottomChild)),
		WithRatio(0.6),
		WithVerticalRatio(0.75),
	)

	scr := uv.NewScreenBuffer(30, 12)
	area := image.Rect(4, 2, 24, 10) // 20x8 at offset (4, 2)
	cur := sp.Draw(scr, area)

	// Vertical split: topHeight = int(8 * 0.75) = 6, bottomHeight = 2.
	// Horizontal split: leftWidth = int(20 * 0.6) = 12, rightWidth = 8.
	assert.Equal(t, image.Rect(4, 2, 16, 8), leftChild.lastArea)
	assert.Equal(t, image.Rect(16, 2, 24, 8), rightChild.lastArea)
	assert.Equal(t, image.Rect(4, 8, 24, 10), bottomChild.lastArea)

	// Non-overlapping and exact coverage of area.
	assert.False(t, leftChild.lastArea.Overlaps(rightChild.lastArea))
	assert.False(t, leftChild.lastArea.Overlaps(bottomChild.lastArea))
	assert.False(t, rightChild.lastArea.Overlaps(bottomChild.lastArea))

	// Rendered cells appear at expected screen positions.
	assert.Equal(t, "L", scr.CellAt(4, 2).Content)
	assert.Equal(t, "R", scr.CellAt(16, 2).Content)
	assert.Equal(t, "B", scr.CellAt(4, 8).Content)

	// Focused right panel's cursor is preferred and translated into screen coords:
	// rightChild.lastArea.Min=(16, 2) + (2, 1) = (18, 3).
	require.NotNil(t, cur)
	assert.Equal(t, 18, cur.X)
	assert.Equal(t, 3, cur.Y)

	// Clearing right panel gives left panel the full top width.
	sp.ClearRightPanel()
	scr2 := uv.NewScreenBuffer(30, 12)
	cur2 := sp.Draw(scr2, area)
	assert.Equal(t, image.Rect(4, 2, 24, 8), leftChild.lastArea)
	assert.Equal(t, image.Rect(4, 8, 24, 10), bottomChild.lastArea)
	require.NotNil(t, cur2)
	assert.Equal(t, 5, cur2.X) // leftChild Min.X(4) + 1
	assert.Equal(t, 2, cur2.Y)

	// Standalone View() works as well.
	v := sp.View()
	assert.Contains(t, v.Content, "LEFT")
	assert.Contains(t, v.Content, "BOTTOM")
}
