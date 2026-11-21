package logs

import (
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/render"
	"github.com/stretchr/testify/require"
)

var (
	_ TableComponent         = (*tableCmp)(nil)
	_ DetailComponent        = (*detailCmp)(nil)
	_ layout.Drawable        = (*tableCmp)(nil)
	_ layout.Drawable        = (*detailCmp)(nil)
	_ render.WheelScrollable = (*detailCmp)(nil)
)

func TestLogsTableSizeUpdateAndDrawCache(t *testing.T) {
	w := logging.NewWriter()
	t1 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 1, 2, 10, 1, 0, 0, time.UTC)

	_, err := fmt.Fprintf(w, "time=%s level=info msg=\"older log entry\" component=db\n", t1.Format(time.RFC3339))
	require.NoError(t, err)
	time.Sleep(time.Millisecond)
	_, err = fmt.Fprintf(w, "time=%s level=error msg=\"newer log entry\" component=api\n", t2.Format(time.RFC3339))
	require.NoError(t, err)

	tbl := NewLogsTable()
	tbl.Init()

	require.NotEmpty(t, tbl.BindingKeys())

	tbl.SetSize(80, 20)
	gotW, gotH := tbl.GetSize()
	require.Equal(t, 80, gotW)
	require.Equal(t, 20, gotH)

	// Send pubsub event to refresh rows.
	m, cmd := tbl.Update(pubsub.Event[logging.LogMessage]{Type: pubsub.CreatedEvent})
	require.Nil(t, cmd)
	tbl = m.(TableComponent)

	// Verify newest row is first.
	tc := tbl.(*tableCmp)
	rows := tc.table.Rows()
	require.GreaterOrEqual(t, len(rows), 2)
	require.Equal(t, "newer log entry", rows[0][3])

	// Render View() and Draw() onto a uv.ScreenBuffer(80, 20).
	view := tbl.View()
	require.NotEmpty(t, view.Content)

	buf := uv.NewScreenBuffer(80, 20)
	cur := tbl.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, tc.drawCache.Misses())
	require.Equal(t, 0, tc.drawCache.Hits())

	rendered := buf.Render()
	require.Contains(t, rendered, "Message")
	require.Contains(t, rendered, "newer log")

	// Second Draw with unchanged state must hit DrawCache.
	cur = tbl.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, tc.drawCache.Misses())
	require.Equal(t, 1, tc.drawCache.Hits())

	// Move selection down and verify selectedLogMsg is emitted and cache updates.
	m, cmd = tbl.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	tbl = m.(TableComponent)
	require.NotNil(t, cmd)
	selMsg := collectSelectedLogMsg(cmd)
	require.NotNil(t, selMsg)
	require.Equal(t, "older log entry", selMsg.Message)

	cur = tbl.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 2, tc.drawCache.Misses())
	require.Equal(t, 1, tc.drawCache.Hits())

	// Empty draw area and small sizes should not panic.
	require.Nil(t, tbl.Draw(buf, image.Rectangle{}))
	tbl.SetSize(0, 0)
	require.Nil(t, tbl.Draw(buf, buf.Bounds()))
}

func TestLogsDetailsSizeUpdateScrollAndDrawCache(t *testing.T) {
	det := NewLogsDetails()
	det.Init()

	require.NotEmpty(t, det.BindingKeys())

	det.SetSize(80, 10)
	gotW, gotH := det.GetSize()
	require.Equal(t, 80, gotW)
	require.Equal(t, 10, gotH)

	// Build a log entry with enough attributes to exceed viewport height for scroll testing.
	attrs := make([]logging.Attr, 0, 25)
	for idx := range 25 {
		attrs = append(attrs, logging.Attr{
			Key:   fmt.Sprintf("attr_%02d", idx),
			Value: fmt.Sprintf("value_%02d", idx),
		})
	}
	entry := logging.LogMessage{
		ID:         "log-scroll-1",
		Time:       time.Date(2026, 1, 2, 12, 34, 56, 0, time.UTC),
		Level:      "warn",
		Message:    "detailed warning message",
		Attributes: attrs,
	}

	m, cmd := det.Update(selectedLogMsg(entry))
	require.Nil(t, cmd)
	det = m.(DetailComponent)

	dc := det.(*detailCmp)
	require.Equal(t, 0, dc.viewport.YOffset())

	view := det.View()
	require.Contains(t, view.Content, "detailed warning message")
	require.Contains(t, view.Content, "attr_00")

	buf := uv.NewScreenBuffer(80, 20)
	cur := det.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, dc.drawCache.Misses())
	require.Equal(t, 0, dc.drawCache.Hits())
	require.Contains(t, buf.Render(), "detailed warning message")

	// Second Draw with unchanged state must hit DrawCache.
	cur = det.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, dc.drawCache.Misses())
	require.Equal(t, 1, dc.drawCache.Hits())

	// Scroll down with CoalescedWheelMsg.
	m, cmd = det.Update(render.CoalescedWheelMsg{DeltaY: 2})
	require.Nil(t, cmd)
	det = m.(DetailComponent)
	afterWheelOffset := dc.viewport.YOffset()
	require.Greater(t, afterWheelOffset, 0)

	// Drawing after scroll misses cache and re-renders.
	cur = det.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 2, dc.drawCache.Misses())
	require.Equal(t, 1, dc.drawCache.Hits())

	// Scroll back up with CoalescedWheelMsg.
	m, _ = det.Update(render.CoalescedWheelMsg{DeltaY: -2})
	det = m.(DetailComponent)
	require.Equal(t, 0, dc.viewport.YOffset())

	// Forward key press down to viewport.
	m, _ = det.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	det = m.(DetailComponent)
	require.Greater(t, dc.viewport.YOffset(), 0)

	// Selecting a new log resets scroll to top and renders all level styles cleanly.
	for _, lvl := range []string{"info", "error", "debug", "custom"} {
		next := logging.LogMessage{
			ID:      "log-" + lvl,
			Time:    time.Date(2026, 1, 2, 13, 0, 0, 0, time.UTC),
			Level:   lvl,
			Message: "message for " + lvl,
		}
		m, _ = det.Update(selectedLogMsg(next))
		det = m.(DetailComponent)
		require.Equal(t, 0, dc.viewport.YOffset())
		require.True(t, strings.Contains(det.View().Content, "message for "+lvl))
	}

	// Empty area and zero size do not panic.
	require.Nil(t, det.Draw(buf, image.Rectangle{}))
	det.SetSize(0, 0)
	require.Nil(t, det.Draw(buf, buf.Bounds()))
}

func collectSelectedLogMsg(cmd tea.Cmd) *selectedLogMsg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch m := msg.(type) {
	case selectedLogMsg:
		return &m
	case tea.BatchMsg:
		for _, sub := range m {
			if found := collectSelectedLogMsg(sub); found != nil {
				return found
			}
		}
	}
	return nil
}
