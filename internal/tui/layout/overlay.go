package layout

import (
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Scrollbar glyph defaults matching Crush's scrollbar rendering.
const (
	ScrollbarThumb = "┃"
	ScrollbarTrack = "│"
)

// CenterRect returns a new [uv.Rectangle] centered within area with the
// specified width and height.
func CenterRect(area uv.Rectangle, width, height int) uv.Rectangle {
	centerX := area.Min.X + area.Dx()/2
	centerY := area.Min.Y + area.Dy()/2
	minX := centerX - width/2
	minY := centerY - height/2
	maxX := minX + width
	maxY := minY + height
	return image.Rect(minX, minY, maxX, maxY)
}

// BottomLeftRect returns a new [uv.Rectangle] positioned at the bottom-left
// within area with the specified width and height.
func BottomLeftRect(area uv.Rectangle, width, height int) uv.Rectangle {
	minX := area.Min.X
	maxX := minX + width
	maxY := area.Max.Y
	minY := maxY - height
	return image.Rect(minX, minY, maxX, maxY)
}

// ClearArea zeros every cell inside area on scr.
func ClearArea(scr uv.Screen, area uv.Rectangle) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			scr.SetCell(x, y, nil)
		}
	}
}

// DrawAt draws the ANSI-styled view string onto scr within area.
func DrawAt(scr uv.Screen, area uv.Rectangle, view string) {
	uv.NewStyledString(view).Draw(scr, area)
}

// DrawCenter draws the given string view centered in area.
func DrawCenter(scr uv.Screen, area uv.Rectangle, view string) {
	DrawCenterCursor(scr, area, view, nil)
}

// DrawCenterCursor draws the given string view centered in area and translates
// cur (if non-nil) from view-local coordinates to screen coordinates. Content
// larger than area is clamped to fit.
func DrawCenterCursor(scr uv.Screen, area uv.Rectangle, view string, cur *tea.Cursor) {
	width, height := lipgloss.Size(view)
	width = min(width, area.Dx())
	height = min(height, area.Dy())
	center := CenterRect(area, width, height)
	if cur != nil {
		cur.X += center.Min.X
		cur.Y += center.Min.Y
	}
	uv.NewStyledString(view).Draw(scr, center)
}

// Scrollbar renders a vertical scrollbar based on content and viewport size.
// Returns an empty string if content fits within viewport (no scrolling needed).
func Scrollbar(height, contentSize, viewportSize, offset int, thumbStyle, trackStyle lipgloss.Style) string {
	if height <= 0 || contentSize <= viewportSize {
		return ""
	}

	// Calculate thumb size (minimum 1 character).
	thumbSize := max(1, height*viewportSize/contentSize)

	// Calculate thumb position.
	maxOffset := contentSize - viewportSize
	if maxOffset <= 0 {
		return ""
	}

	offset = max(0, offset)
	trackSpace := height - thumbSize
	thumbPos := 0
	if trackSpace > 0 && maxOffset > 0 {
		thumbPos = min(trackSpace, offset*trackSpace/maxOffset)
	}

	var sb strings.Builder
	for i := range height {
		if i > 0 {
			sb.WriteString("\n")
		}
		if i >= thumbPos && i < thumbPos+thumbSize {
			sb.WriteString(thumbStyle.Render(ScrollbarThumb))
		} else {
			sb.WriteString(trackStyle.Render(ScrollbarTrack))
		}
	}

	return sb.String()
}
