package render

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DrawCache holds the pre-decoded form of a rendered ANSI string in a
// [uv.ScreenBuffer]. The cache is keyed by the byte-identical rendered string
// and the destination screen's [ansi.Method] (graphemes vs wcwidth pick
// different decoders inside ultraviolet).
//
// On hit, Draw clears the destination area and blits the cached cell buffer in
// O(cells) with zero ANSI re-parsing. The zero value is ready for use.
type DrawCache struct {
	valid    bool
	rendered string
	method   ansi.Method
	buf      uv.ScreenBuffer

	hits   int
	misses int
}

// NewDrawCache creates an empty [DrawCache].
func NewDrawCache() *DrawCache {
	return &DrawCache{}
}

// Draw renders the ANSI-styled string into scr within area. On a cache hit
// (byte-identical rendered string and matching width method), ANSI decoding is
// skipped and the cached ScreenBuffer is blitted directly into scr.
func (c *DrawCache) Draw(scr uv.Screen, area uv.Rectangle, rendered string) {
	method, ok := scr.WidthMethod().(ansi.Method)
	if !ok {
		c.misses++
		uv.NewStyledString(rendered).Draw(scr, area)
		return
	}

	if c.valid && c.rendered == rendered && c.method == method {
		c.hits++
	} else {
		c.misses++
		c.populate(rendered, method)
	}

	drawCachedBuffer(scr, area, c.buf)
}

// Hits returns the cumulative number of DrawCache hits.
func (c *DrawCache) Hits() int {
	return c.hits
}

// Misses returns the cumulative number of DrawCache misses.
func (c *DrawCache) Misses() int {
	return c.misses
}

// Invalidate drops the cached buffer so the next Draw re-decodes its input.
func (c *DrawCache) Invalidate() {
	c.valid = false
	c.rendered = ""
	c.buf = uv.ScreenBuffer{}
}

func (c *DrawCache) populate(rendered string, method ansi.Method) {
	w, h := renderedBounds(rendered, method)
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	buf := uv.NewScreenBuffer(w, h)
	buf.Method = method
	uv.NewStyledString(rendered).Draw(buf, buf.Bounds())

	c.valid = true
	c.rendered = rendered
	c.method = method
	c.buf = buf
}

// renderedBounds returns the (width, height) cell extent of rendered when laid
// out by method. Width is the widest line's StringWidth under method; height is
// the line count.
func renderedBounds(rendered string, method ansi.Method) (w, h int) {
	for line := range strings.SplitSeq(rendered, "\n") {
		w = max(w, method.StringWidth(line))
		h++
	}
	return w, h
}

// drawCachedBuffer blits a previously-decoded buffer into scr at area.
// The area is cleared first because Buffer.Draw skips empty cells, matching
// uv.StyledString.Draw's clearing semantics.
func drawCachedBuffer(scr uv.Screen, area uv.Rectangle, buf uv.ScreenBuffer) {
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			scr.SetCell(x, y, nil)
		}
	}
	buf.Draw(scr, area)
}
