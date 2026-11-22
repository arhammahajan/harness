package list

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// trackedItem is a test helper that counts Render calls.
type trackedItem struct {
	*Versioned
	id         string
	body       string
	focused    bool
	renderHits int
}

func newTrackedItem(id, body string) *trackedItem {
	return &trackedItem{
		Versioned: NewVersioned(),
		id:        id,
		body:      body,
	}
}

func (t *trackedItem) Render(width int) string {
	t.renderHits++
	prefix := ""
	if t.focused {
		prefix = ">"
	}
	return prefix + t.body + ":w=" + strconv.Itoa(width)
}

func (t *trackedItem) SetFocused(focused bool) {
	if t.focused != focused {
		t.focused = focused
		t.Bump()
	}
}

// TestList_LazyRender_OffscreenItemsNotRendered verifies that Render() only
// renders items visible within the viewport budget and leaves off-screen items
// completely untouched.
func TestList_LazyRender_OffscreenItemsNotRendered(t *testing.T) {
	t.Parallel()

	const total = 100
	const viewport = 5

	tracked := make([]*trackedItem, total)
	items := make([]Item, total)
	for i := range total {
		it := newTrackedItem(strconv.Itoa(i), "item-"+strconv.Itoa(i))
		tracked[i] = it
		items[i] = it
	}

	l := NewList(items...)
	l.SetSize(40, viewport)

	out := l.Render()
	require.Len(t, strings.Split(out, "\n"), viewport)
	for i := range viewport {
		require.Equal(t, 1, tracked[i].renderHits, "visible item %d should render once", i)
	}
	for i := viewport; i < total; i++ {
		require.Equal(t, 0, tracked[i].renderHits, "off-screen item %d must not render", i)
	}

	// Scroll directly to bottom: only the bottom viewport items should render.
	l.ScrollToBottom()
	_ = l.Render()
	for i := viewport; i < total-viewport-1; i++ {
		require.Equal(t, 0, tracked[i].renderHits, "middle off-screen item %d must remain unrendered", i)
	}
	for i := total - viewport; i < total; i++ {
		require.Equal(t, 1, tracked[i].renderHits, "bottom visible item %d should render once", i)
	}
}

// TestList_RenderMemo_PointerKey covers the invariant that the list-level cache
// is keyed by item pointer, not slice index, so PrependItems and AppendItems do
// not shift cached entries to the wrong item.
func TestList_RenderMemo_PointerKey(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	c := newTrackedItem("c", "charlie")

	l := NewList(a, b, c)
	l.SetSize(40, 10)

	// First render populates the cache for every item.
	first := l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)
	require.Equal(t, 1, c.renderHits)

	// Prepending a new item must not shift the existing entries to the wrong key.
	z := newTrackedItem("z", "zulu")
	l.PrependItems(z)
	l.ScrollToTop()
	_ = l.Render()
	require.Equal(t, 1, z.renderHits, "prepended item rendered once")
	require.Equal(t, 1, a.renderHits, "stable item must keep its cached entry across PrependItems")
	require.Equal(t, 1, b.renderHits, "stable item must keep its cached entry across PrependItems")
	require.Equal(t, 1, c.renderHits, "stable item must keep its cached entry across PrependItems")

	// AppendItems is symmetric.
	d := newTrackedItem("d", "delta")
	l.AppendItems(d)
	_ = l.Render()
	require.Equal(t, 1, d.renderHits, "appended item rendered once")
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)
	require.Equal(t, 1, c.renderHits)

	require.Contains(t, first, "alpha")
}

// TestList_SetSize_WidthChangeInvalidates covers the invariant that a width
// change drops every cached entry but a height-only change leaves the cache intact.
func TestList_SetSize_WidthChangeInvalidates(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")

	l := NewList(a, b)
	l.SetSize(40, 10)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)

	// Height-only change: no invalidation.
	l.SetSize(40, 20)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits, "height-only change must keep cache entries")
	require.Equal(t, 1, b.renderHits, "height-only change must keep cache entries")

	// Width change: every entry invalidates.
	l.SetSize(80, 20)
	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "width change must invalidate cache entries")
	require.Equal(t, 2, b.renderHits, "width change must invalidate cache entries")
}

// TestList_RemoveItem_DropsEntry covers the invariant that RemoveItem drops the
// cache entry for the removed item but leaves surviving entries in place.
func TestList_RemoveItem_DropsEntry(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	c := newTrackedItem("c", "charlie")

	l := NewList(a, b, c)
	l.SetSize(40, 10)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)
	require.Equal(t, 1, c.renderHits)

	l.RemoveItem(1) // remove b
	_ = l.Render()
	require.Equal(t, 1, a.renderHits, "stable item must keep cached entry across RemoveItem")
	require.Equal(t, 1, c.renderHits, "stable item must keep cached entry across RemoveItem")

	// Re-adding b confirms its entry was dropped.
	l.AppendItems(b)
	_ = l.Render()
	require.Equal(t, 2, b.renderHits, "re-added item must re-render")
}

// TestList_Cache_NotReRendered pins that a cached item is never re-rendered
// until a width change, explicit Invalidate, or version bump.
func TestList_Cache_NotReRendered(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")

	l := NewList(a, b)
	l.SetSize(40, 10)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)

	for range 5 {
		_ = l.Render()
	}
	require.Equal(t, 1, a.renderHits, "cached items must not re-render across redraws")
	require.Equal(t, 1, b.renderHits, "cached items must not re-render across redraws")

	// Explicit Invalidate drops only the targeted item.
	l.Invalidate(a)
	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "invalidated item must re-render")
	require.Equal(t, 1, b.renderHits, "other item remains cached")
}

// TestList_Cache_VersionBumpInvalidates pins that a version bump re-renders the
// item so no stale output is served.
func TestList_Cache_VersionBumpInvalidates(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	l := NewList(a)
	l.SetSize(40, 10)

	_ = l.Render()
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)

	a.Bump()
	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "version bump must invalidate the entry")

	_ = l.Render()
	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "post-bump render is cached again")
}

// TestList_ItemsVersion verifies ItemsVersion detects any item version bump
// without invoking Render on any item.
func TestList_ItemsVersion(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	l := NewList(a, b)
	l.SetSize(40, 10)

	v0 := l.ItemsVersion()
	require.Equal(t, 0, a.renderHits, "ItemsVersion must not render items")
	require.Equal(t, 0, b.renderHits, "ItemsVersion must not render items")

	b.Bump()
	v1 := l.ItemsVersion()
	require.NotEqual(t, v0, v1, "ItemsVersion must change when an item bumps")
	require.Equal(t, 0, a.renderHits)
	require.Equal(t, 0, b.renderHits)

	a.Bump()
	v2 := l.ItemsVersion()
	require.NotEqual(t, v1, v2)
}

// TestList_Cache_SelectionDrag pins that beginning and ending a selection drag
// each re-render the items inside its range once.
func TestList_Cache_SelectionDrag(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	c := newTrackedItem("c", "charlie")

	l := NewList(a, b, c)
	l.SetSize(40, 10)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)
	require.Equal(t, 1, c.renderHits)

	l.BeginSelectionDrag(0, 1)
	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "drag-spanned item must re-render once on entering the drag")
	require.Equal(t, 2, b.renderHits, "drag-spanned item must re-render once on entering the drag")
	require.Equal(t, 1, c.renderHits, "out-of-range item stays cached")

	_ = l.Render()
	require.Equal(t, 2, a.renderHits, "version-stable item hits the cache")
	require.Equal(t, 2, b.renderHits, "version-stable item hits the cache")

	l.EndSelectionDrag()
	_ = l.Render()
	require.Equal(t, 3, a.renderHits, "ending the drag re-renders the entry")
	require.Equal(t, 3, b.renderHits, "ending the drag re-renders the entry")

	for range 3 {
		_ = l.Render()
	}
	require.Equal(t, 3, a.renderHits, "cached after drag end")
	require.Equal(t, 3, b.renderHits, "cached after drag end")
}

// TestList_RenderOutputStableAcrossDraws verifies rendering the same list
// multiple times produces byte-identical output.
func TestList_RenderOutputStableAcrossDraws(t *testing.T) {
	t.Parallel()

	items := make([]Item, 0, 5)
	for i := range 5 {
		items = append(items, newTrackedItem(strconv.Itoa(i), "item-"+strconv.Itoa(i)))
	}
	l := NewList(items...)
	l.SetSize(40, 20)

	first := l.Render()
	for range 4 {
		require.Equal(t, first, l.Render(), "render output must be byte-stable across draws")
	}
	require.True(t, strings.Contains(first, "item-0"))
}

// TestList_SetItems_PointerOverlapRetainsCache verifies SetItems keeps cache
// entries for surviving item pointers and drops entries for removed ones.
func TestList_SetItems_PointerOverlapRetainsCache(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	c := newTrackedItem("c", "charlie")
	d := newTrackedItem("d", "delta")

	l := NewList(a, b, c)
	l.SetSize(40, 10)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits)
	require.Equal(t, 1, b.renderHits)
	require.Equal(t, 1, c.renderHits)

	l.SetItems(a, c, d)
	_ = l.Render()
	require.Equal(t, 1, a.renderHits, "stable item must keep cached entry across SetItems")
	require.Equal(t, 1, c.renderHits, "stable item must keep cached entry across SetItems")
	require.Equal(t, 1, d.renderHits, "new item renders once")

	l.SetItems(a, b, c)
	_ = l.Render()
	require.Equal(t, 2, b.renderHits, "re-introduced item must re-render — its old entry was dropped")
	require.Equal(t, 1, a.renderHits, "stable item retained across multiple SetItems")
	require.Equal(t, 1, c.renderHits, "stable item retained across multiple SetItems")
}

// TestList_SetItems_AllNewDropsEveryEntry covers replacing all items and
// clearing to empty followed by AppendItems.
func TestList_SetItems_AllNewDropsEveryEntry(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	b := newTrackedItem("b", "bravo")
	c := newTrackedItem("c", "charlie")

	l := NewList(a, b, c)
	l.SetSize(40, 10)
	_ = l.Render()

	x := newTrackedItem("x", "xray")
	y := newTrackedItem("y", "yankee")
	l.SetItems(x, y)
	_ = l.Render()
	require.Equal(t, 1, x.renderHits)
	require.Equal(t, 1, y.renderHits)

	// Clear to empty, then append: offsetIdx must remain valid (0).
	l.SetItems()
	require.Equal(t, "", l.Render())
	oIdx, oLine := l.ScrollPosition()
	require.Equal(t, 0, oIdx)
	require.Equal(t, 0, oLine)

	l.AppendItems(a, b, c)
	out := l.Render()
	require.Contains(t, out, "alpha")
	require.Equal(t, 2, a.renderHits, "previously-dropped item must re-render")
	require.Equal(t, 2, b.renderHits, "previously-dropped item must re-render")
	require.Equal(t, 2, c.renderHits, "previously-dropped item must re-render")
}

// TestVersioned_BumpMonotonic covers the basic Versioned contract.
func TestVersioned_BumpMonotonic(t *testing.T) {
	t.Parallel()

	v := NewVersioned()
	require.Equal(t, uint64(0), v.Version())
	v.Bump()
	require.Equal(t, uint64(1), v.Version())
	v.Bump()
	v.Bump()
	require.Equal(t, uint64(3), v.Version())
}

// multiLineItem is a test helper whose Render returns a fixed multi-line body.
type multiLineItem struct {
	*Versioned
	id     string
	height int
}

func newMultiLineItem(id string, height int) *multiLineItem {
	return &multiLineItem{
		Versioned: NewVersioned(),
		id:        id,
		height:    height,
	}
}

func (m *multiLineItem) Render(_ int) string {
	if m.height <= 0 {
		return ""
	}
	parts := make([]string, m.height)
	for i := range m.height {
		parts[i] = m.id + ":" + strconv.Itoa(i)
	}
	return strings.Join(parts, "\n")
}

// expectedRender computes what list.Render should produce from first principles.
func expectedRender(items []*multiLineItem, height, offsetIdx, offsetLine, gap int, reverse bool) string {
	if len(items) == 0 {
		return ""
	}
	budget := max(height, 0)
	var lines []string
	currentOffset := offsetLine
	for idx := offsetIdx; idx < len(items) && len(lines) < budget; idx++ {
		body := items[idx].Render(0)
		body = strings.TrimRight(body, "\n")
		itemLines := strings.Split(body, "\n")
		itemHeight := len(itemLines)

		if currentOffset >= 0 && currentOffset < itemHeight {
			lines = append(lines, itemLines[currentOffset:]...)
			for range gap {
				lines = append(lines, "")
			}
		} else {
			gapOffset := currentOffset - itemHeight
			gapRemaining := gap - gapOffset
			for range max(gapRemaining, 0) {
				lines = append(lines, "")
			}
		}
		currentOffset = 0
	}
	if len(lines) > budget {
		lines = lines[:budget]
	}
	if reverse {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// TestList_BoundedRender_ByteIdentityMatrix sweeps over
// (item heights x viewport heights x offsets x gaps x reverse).
func TestList_BoundedRender_ByteIdentityMatrix(t *testing.T) {
	t.Parallel()

	itemHeights := [][]int{
		{1},
		{5},
		{1, 1, 1},
		{3, 7, 2},
		{20, 5, 30},
		{50, 1, 50, 1},
	}
	viewportHeights := []int{1, 3, 5, 10, 25, 100}
	offsetIdxs := []int{0, 1, 2}
	offsetLines := []int{0, 1, 4}
	gaps := []int{0, 1, 3}
	reverses := []bool{false, true}

	for _, heights := range itemHeights {
		for _, vh := range viewportHeights {
			for _, oIdx := range offsetIdxs {
				if oIdx >= len(heights) {
					continue
				}
				for _, oLine := range offsetLines {
					maxOffset := heights[oIdx]
					if oLine >= maxOffset {
						continue
					}
					for _, gap := range gaps {
						for _, reverse := range reverses {
							items := make([]*multiLineItem, len(heights))
							asItems := make([]Item, len(heights))
							for i, h := range heights {
								items[i] = newMultiLineItem("i"+strconv.Itoa(i), h)
								asItems[i] = items[i]
							}
							l := NewList(asItems...)
							l.SetSize(40, vh)
							l.SetGap(gap)
							l.SetReverse(reverse)
							l.offsetIdx = oIdx
							l.offsetLine = oLine

							got := l.Render()
							want := expectedRender(items, vh, oIdx, oLine, gap, reverse)
							if n := strings.Count(want, "\n") + 1; n < vh && (oIdx > 0 || oLine > 0) {
								cIdx, cLine := l.ScrollPosition()
								want = expectedRender(items, vh, cIdx, cLine, gap, reverse)
								if cIdx > 0 || cLine > 0 {
									require.Equalf(t, vh, strings.Count(got, "\n")+1,
										"clamped render underfilled heights=%v vh=%d oIdx=%d oLine=%d gap=%d",
										heights, vh, oIdx, oLine, gap)
								}
							}
							require.Equalf(t, want, got,
								"mismatch heights=%v vh=%d oIdx=%d oLine=%d gap=%d reverse=%v",
								heights, vh, oIdx, oLine, gap, reverse)
						}
					}
				}
			}
		}
	}
}

func TestList_GiantItemBoundedRender(t *testing.T) {
	t.Parallel()

	const itemHeight = 10000
	const viewport = 50

	giant := newMultiLineItem("giant", itemHeight)
	l := NewList(giant)
	l.SetSize(40, viewport)

	out := l.Render()
	got := strings.Count(out, "\n") + 1
	require.Equal(t, viewport, got)

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		require.Equal(t, "giant:"+strconv.Itoa(i), line)
	}
}

func TestList_GiantItemWithOffsetBoundedRender(t *testing.T) {
	t.Parallel()

	const itemHeight = 10000
	const viewport = 50
	const offset = 1234

	giant := newMultiLineItem("giant", itemHeight)
	l := NewList(giant)
	l.SetSize(40, viewport)
	l.offsetLine = offset

	out := l.Render()
	lines := strings.Split(out, "\n")
	require.Len(t, lines, viewport)
	for i, line := range lines {
		require.Equal(t, "giant:"+strconv.Itoa(offset+i), line)
	}
}

func TestList_GapOverflow(t *testing.T) {
	t.Parallel()

	a := newMultiLineItem("a", 10)
	b := newMultiLineItem("b", 10)
	l := NewList(a, b)
	l.SetSize(40, 5)
	l.SetGap(3)

	out := l.Render()
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 5)
	for i, line := range lines {
		require.Equal(t, "a:"+strconv.Itoa(i), line)
	}
}

func TestList_GapOverflow_BoundaryStraddle(t *testing.T) {
	t.Parallel()

	a := newMultiLineItem("a", 10)
	b := newMultiLineItem("b", 10)
	l := NewList(a, b)
	l.SetSize(40, 12)
	l.SetGap(3)

	out := l.Render()
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 12)
	for i := range 10 {
		require.Equal(t, "a:"+strconv.Itoa(i), lines[i])
	}
	require.Equal(t, "", lines[10])
	require.Equal(t, "", lines[11])
}

func TestList_ReverseGiantItem(t *testing.T) {
	t.Parallel()

	const itemHeight = 10000
	const viewport = 50

	giant := newMultiLineItem("giant", itemHeight)
	l := NewList(giant)
	l.SetSize(40, viewport)
	l.SetReverse(true)

	out := l.Render()
	lines := strings.Split(out, "\n")
	require.Len(t, lines, viewport)
	for i, line := range lines {
		expectedIdx := viewport - 1 - i
		require.Equal(t, "giant:"+strconv.Itoa(expectedIdx), line)
	}
}

func TestList_OffsetLineAtItemBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		gap      int
		viewport int
		want     []string
	}{
		{
			name:     "gap zero jumps straight to next item",
			gap:      0,
			viewport: 5,
			want:     []string{"b:0", "b:1", "b:2", "b:3", "b:4"},
		},
		{
			name:     "gap two emits gap rows then next item",
			gap:      2,
			viewport: 5,
			want:     []string{"", "", "b:0", "b:1", "b:2"},
		},
	}

	const itemHeight = 4
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := newMultiLineItem("a", itemHeight)
			b := newMultiLineItem("b", 10)
			l := NewList(a, b)
			l.SetSize(40, tc.viewport)
			l.SetGap(tc.gap)
			l.offsetIdx = 0
			l.offsetLine = itemHeight

			out := l.Render()
			require.Equal(t, strings.Join(tc.want, "\n"), out)
		})
	}
}

func TestList_OffsetLineInsideGap(t *testing.T) {
	t.Parallel()

	const itemHeight = 4
	const gap = 3
	const viewport = 5

	a := newMultiLineItem("a", itemHeight)
	b := newMultiLineItem("b", 10)
	l := NewList(a, b)
	l.SetSize(40, viewport)
	l.SetGap(gap)
	l.offsetIdx = 0
	l.offsetLine = itemHeight + 1

	out := l.Render()
	want := strings.Join([]string{"", "", "b:0", "b:1", "b:2"}, "\n")
	require.Equal(t, want, out)
}

func TestList_ViewportZeroOrNegative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		height int
	}{
		{name: "height zero", height: 0},
		{name: "height negative", height: -1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := newMultiLineItem("a", 5)
			l := NewList(a)
			l.SetSize(40, tc.height)

			out := l.Render()
			require.Equal(t, "", out)
			require.Equal(t, 0, l.height)
		})
	}
}

// TestList_Prewarm covers incremental cache warming.
func TestList_Prewarm(t *testing.T) {
	t.Parallel()

	items := make([]Item, 6)
	tracked := make([]*trackedItem, 6)
	for i := range items {
		it := newTrackedItem(strconv.Itoa(i), "body")
		tracked[i] = it
		items[i] = it
	}
	l := NewList(items...)
	l.SetSize(40, 3)

	next := l.Prewarm(0, 3)
	require.Equal(t, 3, next)
	for i := 0; i < 3; i++ {
		require.Equal(t, 1, tracked[i].renderHits, "warmed item %d", i)
	}
	for i := 3; i < 6; i++ {
		require.Equal(t, 0, tracked[i].renderHits, "unwarmed item %d", i)
	}

	l.Prewarm(next, 3)
	before := totalRenderHits(tracked)
	_ = l.TotalHeight()
	require.Equal(t, before, totalRenderHits(tracked), "TotalHeight re-rendered after full warm")

	l.SetSize(40, 3)
	_ = l.TotalHeight()
	require.Equal(t, before, totalRenderHits(tracked), "same-width SetSize dropped the warmed cache")
}

// TestList_Overflows covers the cheap bounded overflow check, verifying it
// stops early from the bottom rather than rendering all items.
func TestList_Overflows(t *testing.T) {
	t.Parallel()

	tracked := make([]*trackedItem, 20)
	items := make([]Item, 20)
	for i := range items {
		it := newTrackedItem(strconv.Itoa(i), "body") // 1 line each
		tracked[i] = it
		items[i] = it
	}
	l := NewList(items...)
	l.SetSize(40, 4)

	require.True(t, l.Overflows(4), "20 one-line items overflow a height of 4")
	// Walking from the bottom crosses height=4 after 5 items (indices 19..15).
	require.Equal(t, 5, totalRenderHits(tracked), "Overflows should stop once threshold is exceeded")
	require.Equal(t, 0, tracked[0].renderHits, "top items must not be rendered by Overflows")

	require.False(t, l.Overflows(30), "20 one-line items fit within a height of 30")
}

func totalRenderHits(items []*trackedItem) int {
	n := 0
	for _, it := range items {
		n += it.renderHits
	}
	return n
}

type tallItem struct {
	*Versioned
	lines int
}

func newTallItem(lines int) *tallItem {
	return &tallItem{Versioned: NewVersioned(), lines: lines}
}

func (t *tallItem) Render(width int) string {
	return strings.TrimSuffix(strings.Repeat("x\n", t.lines), "\n")
}

func TestList_AtBottom_TallFirstVisibleItem(t *testing.T) {
	t.Parallel()

	for _, gap := range []int{0, 1} {
		l := NewList(newTallItem(50), newTallItem(3))
		l.SetGap(gap)
		l.SetSize(40, 10)

		l.ScrollToBottom()
		require.True(t, l.AtBottom(), "gap=%d: at bottom after ScrollToBottom", gap)

		l.ScrollBy(-1)
		require.False(t, l.AtBottom(), "gap=%d: not at bottom after scrolling up", gap)

		l.ScrollBy(1)
		require.True(t, l.AtBottom(), "gap=%d: back at bottom after scrolling down", gap)
	}
}

func fiveByFour() (*List, []*multiLineItem) {
	items := make([]*multiLineItem, 0, 6)
	listItems := make([]Item, 0, 6)
	for i := range 6 {
		it := newMultiLineItem(string(rune('a'+i)), 4)
		items = append(items, it)
		listItems = append(listItems, it)
	}
	l := NewList(listItems...)
	l.SetGap(1)
	l.SetSize(20, 8)
	return l, items
}

func TestList_Render_ClampsWhenTailShrinks(t *testing.T) {
	t.Parallel()
	l, items := fiveByFour()
	l.ScrollToBottom()
	l.ScrollBy(-1)

	items[5].height = 1
	items[5].Bump()
	got := strings.Split(l.Render(), "\n")

	require.Len(t, got, 8, "viewport must stay full")
	require.Equal(t, "f:0", got[len(got)-1], "last line must be the end of the content")
	require.True(t, l.AtBottom())
}

func TestList_RemoveItem_TopItemResetsLineOffset(t *testing.T) {
	t.Parallel()
	l, items := fiveByFour()
	items[2].height = 1
	l.offsetIdx, l.offsetLine = 1, 3

	l.RemoveItem(1)
	got := strings.Split(l.Render(), "\n")

	require.Equal(t, "c:0", got[0])
}

func TestList_SetItems_FewerItemsStaysFull(t *testing.T) {
	t.Parallel()
	l, items := fiveByFour()
	l.ScrollToBottom()

	l.SetItems(items[0], items[1], items[2])
	got := strings.Split(l.Render(), "\n")

	require.Len(t, got, 8)
	require.Equal(t, "c:3", got[len(got)-1])
}

func TestList_Restyle_KeepsHeights(t *testing.T) {
	t.Parallel()

	items := make([]*trackedItem, 0, 20)
	listItems := make([]Item, 0, 20)
	for idx := range 20 {
		item := newTrackedItem(strconv.Itoa(idx), "item "+strconv.Itoa(idx))
		items = append(items, item)
		listItems = append(listItems, item)
	}
	l := NewList(listItems...)
	l.SetSize(40, 3)
	l.ScrollToIndex(17)
	_ = l.Render()
	total := l.TotalHeight()
	offset := l.Offset()
	before := totalRenderHits(items)

	// An off-screen item that mutated before the restyle has no current height to keep.
	items[0].Bump()

	restyled := 0
	l.Restyle(func(item Item) {
		restyled++
		item.(*trackedItem).Bump()
	})
	require.Equal(t, len(items), restyled)

	require.Equal(t, total, l.TotalHeight())
	require.Equal(t, offset, l.Offset())
	require.Equal(t, before+1, totalRenderHits(items), "only the mutated item re-renders for its height")

	_ = l.Render()
	require.Equal(t, before+4, totalRenderHits(items), "visible items re-render their content")
	require.Equal(t, 2, items[19].renderHits)
	require.Equal(t, 1, items[10].renderHits, "off-screen items keep their cached render")
}

// TestList_ScrollAndPosition covers ScrollBy, ScrollToTop, ScrollToBottom,
// ScrollToIndex, ScrollToSelected, VisibleItemIndices, ItemAt, and ItemIndexAtPosition.
func TestList_ScrollAndPosition(t *testing.T) {
	t.Parallel()

	l, items := fiveByFour()
	// 6 items * 4 lines + 5 gaps * 1 line = 29 lines total; viewport = 8.
	require.Equal(t, 29, l.TotalHeight())
	require.Equal(t, 6, l.Len())
	require.Equal(t, items[2], l.ItemAt(2))
	require.Nil(t, l.ItemAt(-1))
	require.Nil(t, l.ItemAt(99))

	require.False(t, l.AtBottom())
	start, end := l.VisibleItemIndices()
	require.Equal(t, 0, start)
	require.Equal(t, 1, end)

	// Hit-test y positions: item 0 occupies y=0..3, gap at y=4, item 1 at y=5..7.
	idx, itemY := l.ItemIndexAtPosition(0, 2)
	require.Equal(t, 0, idx)
	require.Equal(t, 2, itemY)
	idx, itemY = l.ItemIndexAtPosition(0, 4) // gap row
	require.Equal(t, -1, idx)
	require.Equal(t, -1, itemY)
	idx, itemY = l.ItemIndexAtPosition(0, 6)
	require.Equal(t, 1, idx)
	require.Equal(t, 1, itemY)

	// Scroll down past the bottom clamps to bottom.
	l.ScrollBy(100)
	require.True(t, l.AtBottom())
	require.Equal(t, 29-8, l.Offset())

	// Scroll up past the top clamps to top.
	l.ScrollBy(-100)
	require.Equal(t, 0, l.Offset())

	// ScrollToIndex + ScrollToSelected.
	l.ScrollToIndex(3)
	oIdx, oLine := l.ScrollPosition()
	require.Equal(t, 3, oIdx)
	require.Equal(t, 0, oLine)

	l.SetSelected(0)
	require.False(t, l.SelectedItemInView())
	l.ScrollToSelected()
	require.True(t, l.SelectedItemInView())
	oIdx, oLine = l.ScrollPosition()
	require.Equal(t, 0, oIdx)
	require.Equal(t, 0, oLine)

	l.SetSelected(5)
	l.ScrollToSelected()
	require.True(t, l.SelectedItemInView())
	require.True(t, l.AtBottom())
}

// TestList_FocusSelectionAndSpacer covers Focus/Blur/Focused, FocusedRenderCallback,
// selection navigation, and SpacerItem.
func TestList_FocusSelectionAndSpacer(t *testing.T) {
	t.Parallel()

	a := newTrackedItem("a", "alpha")
	sp := NewSpacerItem(2)
	b := newTrackedItem("b", "bravo")

	l := NewList(a, sp, b)
	l.SetSize(40, 10)
	l.RegisterRenderCallback(FocusedRenderCallback(l))

	require.False(t, l.Focused())
	require.Equal(t, -1, l.Selected())

	require.True(t, l.SelectFirst())
	require.Equal(t, 0, l.Selected())
	require.True(t, l.IsSelectedFirst())
	require.False(t, l.IsSelectedLast())

	l.Focus()
	require.True(t, l.Focused())
	out := l.Render()
	lines := strings.Split(out, "\n")
	require.Equal(t, ">alpha:w=40", lines[0])
	require.Equal(t, "", lines[1]) // SpacerItem renders a blank line
	require.Equal(t, "bravo:w=40", lines[2])

	require.True(t, l.SelectNext())
	require.Equal(t, 1, l.Selected())
	require.True(t, l.SelectNext())
	require.Equal(t, 2, l.Selected())
	require.True(t, l.IsSelectedLast())
	require.False(t, l.SelectNext())

	out = l.Render()
	require.Contains(t, out, ">bravo:w=40")
	require.NotContains(t, out, ">alpha:w=40")

	require.True(t, l.SelectPrev())
	require.Equal(t, 1, l.Selected())
	require.True(t, l.SelectLast())
	require.Equal(t, 2, l.Selected())

	l.Blur()
	require.False(t, l.Focused())
	out = l.Render()
	require.NotContains(t, out, ">")
}
