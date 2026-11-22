package list

import (
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func TestHighlightContentWrappedLines(t *testing.T) {
	t.Parallel()

	content := "This is a long line that should wrap around"
	width := 20

	result := HighlightContent(content, uv.Rect(0, 0, width, 3), 0, 0, -1, -1)
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	require.Len(t, lines, 1, "wrapped content should be a single logical line")
}

func TestHighlightContentRealNewlinesPreserved(t *testing.T) {
	t.Parallel()

	content := "first\nsecond"
	width := 40

	result := HighlightContent(content, uv.Rect(0, 0, width, lipgloss.Height(content)), 0, 0, -1, -1)
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	require.Len(t, lines, 2, "real newlines should be preserved")
	require.Contains(t, lines[0], "first")
	require.Contains(t, lines[1], "second")
}

func TestHighlightContentParagraphBreak(t *testing.T) {
	t.Parallel()

	content := "first paragraph\n\nsecond paragraph"
	width := 40

	result := HighlightContent(content, uv.Rect(0, 0, width, lipgloss.Height(content)), 0, 0, -1, -1)
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 3, "paragraph break should produce empty line")
}

func TestHighlightContentHardWrap(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("a", 79) + "b"
	width := 80

	result := HighlightContent(content, uv.Rect(0, 0, width, lipgloss.Height(content)), 0, 0, -1, -1)
	require.Equal(t, strings.Repeat("a", 79)+"b\n", result)
}

func TestHighlightContentCodespanPaddingAndRealNBSP(t *testing.T) {
	t.Parallel()

	// CodespanPadding sentinel restores backticks; real NBSP stays verbatim.
	content := "question\u00a0: is " + CodespanPadding + "code" + CodespanPadding + " ok"
	result := HighlightContent(content, uv.Rect(0, 0, 80, 1), 0, 0, -1, -1)
	require.Equal(t, "question\u00a0: is `code` ok\n", result)
}

func TestHighlightAndStyleHelpers(t *testing.T) {
	t.Parallel()

	area := uv.Rect(0, 0, 20, 2)
	// Negative start returns original content unmodified.
	require.Equal(t, "hello", Highlight("hello", area, -1, 0, -1, -1, nil))

	lgStyle := lipgloss.NewStyle().Bold(true).Italic(true).Underline(true).Strikethrough(true).Faint(true).Blink(true).Reverse(true)
	uvStyle := ToStyle(lgStyle)
	require.NotZero(t, uvStyle.Attrs&uv.AttrBold)
	require.NotZero(t, uvStyle.Attrs&uv.AttrItalic)
	require.Equal(t, uv.UnderlineSingle, uvStyle.Underline)
	require.NotZero(t, uvStyle.Attrs&uv.AttrStrikethrough)
	require.NotZero(t, uvStyle.Attrs&uv.AttrFaint)
	require.NotZero(t, uvStyle.Attrs&uv.AttrBlink)
	require.NotZero(t, uvStyle.Attrs&uv.AttrReverse)

	highlighted := Highlight("hello world", area, 0, 0, 0, 5, ToHighlighter(lgStyle))
	require.Contains(t, highlighted, "hello")

	padded := lipgloss.NewStyle().Margin(1, 2, 1, 2).Padding(1, 1, 1, 1)
	adj := AdjustArea(image.Rect(0, 0, 20, 10), padded)
	require.Equal(t, image.Rect(3, 2, 17, 8), adj)
}
