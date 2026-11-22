package core

import (
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/pubsub"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/components/chat"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/util"
	"github.com/stretchr/testify/require"
)

var (
	_ StatusCmp       = (*statusCmp)(nil)
	_ tea.Model       = (*statusCmp)(nil)
	_ layout.Drawable = (*statusCmp)(nil)
)

func TestNewStatusCmpAndWindowSize(t *testing.T) {
	cmp := NewStatusCmp(nil)
	require.NotNil(t, cmp)
	require.Nil(t, cmp.Init())

	m, cmd := cmp.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	require.Nil(t, cmd)
	cmp = m.(StatusCmp)

	sc := cmp.(*statusCmp)
	require.Equal(t, 120, sc.width)

	view := cmp.View()
	require.Contains(t, view.Content, "ctrl+? help")
	require.Contains(t, view.Content, "No diagnostics")
	require.Contains(t, view.Content, "Unknown")
	require.Equal(t, 120, lipgloss.Width(view.Content))
}

func TestStatusCmpInfoMsgAndClearStatusMsg(t *testing.T) {
	cmp := NewStatusCmp(nil)
	m, _ := cmp.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	cmp = m.(StatusCmp)

	// Info toast with explicit short TTL returns a tick cmd that yields ClearStatusMsg.
	m, cmd := cmp.Update(util.InfoMsg{
		Type: util.InfoTypeInfo,
		Msg:  "Saved changes",
		TTL:  5 * time.Millisecond,
	})
	cmp = m.(StatusCmp)
	require.NotNil(t, cmd)
	require.Contains(t, cmp.View().Content, "Saved changes")

	clearMsg := cmd()
	require.IsType(t, util.ClearStatusMsg{}, clearMsg)

	// ClearStatusMsg removes the active toast.
	m, cmd = cmp.Update(clearMsg)
	cmp = m.(StatusCmp)
	require.Nil(t, cmd)
	require.NotContains(t, cmp.View().Content, "Saved changes")

	// Warn and Error toasts with default TTL (TTL == 0) also return a clear cmd.
	for _, tc := range []struct {
		name string
		typ  util.InfoType
		msg  string
	}{
		{name: "warn", typ: util.InfoTypeWarn, msg: "Disk almost full"},
		{name: "error", typ: util.InfoTypeError, msg: "Connection failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cmd := cmp.Update(util.InfoMsg{Type: tc.typ, Msg: tc.msg})
			cmp = m.(StatusCmp)
			require.NotNil(t, cmd)
			require.Contains(t, cmp.View().Content, tc.msg)

			m, cmd = cmp.Update(util.ClearStatusMsg{})
			cmp = m.(StatusCmp)
			require.Nil(t, cmd)
			require.NotContains(t, cmp.View().Content, tc.msg)
		})
	}

	// Long message is truncated with ellipsis.
	longMsg := strings.Repeat("x", 200)
	m, cmd = cmp.Update(util.InfoMsg{Type: util.InfoTypeInfo, Msg: longMsg})
	cmp = m.(StatusCmp)
	require.NotNil(t, cmd)
	view := cmp.View().Content
	require.Contains(t, view, "...")
	require.NotContains(t, view, longMsg)
}

func TestStatusCmpSessionTokensAndFormat(t *testing.T) {
	require.Equal(t, "Context: 500, Cost: $0.12", formatTokensAndCost(500, 100_000, 0.12))
	require.Equal(t, "Context: 2K, Cost: $1.50", formatTokensAndCost(2_000, 100_000, 1.5))
	require.Equal(t, "Context: 1.5K, Cost: $0.05", formatTokensAndCost(1_500, 100_000, 0.05))
	require.Equal(t, "Context: 2M, Cost: $12.34", formatTokensAndCost(2_000_000, 10_000_000, 12.34))
	require.Equal(t, "Context: 1.2M, Cost: $3.00", formatTokensAndCost(1_200_000, 10_000_000, 3.0))
	require.Equal(t, "Context: "+styles.WarningIcon+"(85%), Cost: $2.00", formatTokensAndCost(85_000, 100_000, 2.0))
	require.Equal(t, "Context: 500, Cost: $0.00", formatTokensAndCost(500, 0, 0))

	cmp := NewStatusCmp(nil)
	m, _ := cmp.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	cmp = m.(StatusCmp)

	sess := session.Session{
		ID:               "sess-1",
		PromptTokens:     1200,
		CompletionTokens: 300,
		Cost:             0.42,
	}
	m, cmd := cmp.Update(chat.SessionSelectedMsg(sess))
	require.Nil(t, cmd)
	cmp = m.(StatusCmp)
	require.Contains(t, cmp.View().Content, "Context: 1.5K, Cost: $0.42")

	// UpdatedEvent for matching session refreshes token/cost badge.
	sess.CompletionTokens = 800
	sess.Cost = 0.75
	m, cmd = cmp.Update(pubsub.Event[session.Session]{
		Type:    pubsub.UpdatedEvent,
		Payload: sess,
	})
	require.Nil(t, cmd)
	cmp = m.(StatusCmp)
	require.Contains(t, cmp.View().Content, "Context: 2K, Cost: $0.75")

	// SessionClearedMsg removes the token/cost badge.
	m, cmd = cmp.Update(chat.SessionClearedMsg{})
	require.Nil(t, cmd)
	cmp = m.(StatusCmp)
	require.NotContains(t, cmp.View().Content, "Context:")
}

func TestStatusCmpDrawAndDrawCache(t *testing.T) {
	cmp := NewStatusCmp(nil)
	sc := cmp.(*statusCmp)

	buf := uv.NewScreenBuffer(120, 1)

	// First Draw populates the cache (1 miss, 0 hits).
	cur := cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, sc.drawCache.Misses())
	require.Equal(t, 0, sc.drawCache.Hits())

	rendered := buf.Render()
	require.Contains(t, rendered, "ctrl+? help")
	require.Contains(t, rendered, "No diagnostics")
	require.Contains(t, rendered, "Unknown")

	// Repeated Draws with unchanged state must hit DrawCache.
	cur = cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, sc.drawCache.Misses())
	require.Equal(t, 1, sc.drawCache.Hits())

	cur = cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 1, sc.drawCache.Misses())
	require.Equal(t, 2, sc.drawCache.Hits())

	// Changing state via InfoMsg causes a cache miss and updates rendered buffer.
	m, cmd := cmp.Update(util.InfoMsg{Type: util.InfoTypeInfo, Msg: "Status updated"})
	require.NotNil(t, cmd)
	cmp = m.(StatusCmp)

	cur = cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 2, sc.drawCache.Misses())
	require.Equal(t, 2, sc.drawCache.Hits())
	require.Contains(t, buf.Render(), "Status updated")

	// Repeated Draw with same InfoMsg hits cache again.
	cur = cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 2, sc.drawCache.Misses())
	require.Equal(t, 3, sc.drawCache.Hits())

	// Clearing the status misses cache and removes the message from the buffer.
	m, _ = cmp.Update(util.ClearStatusMsg{})
	cmp = m.(StatusCmp)

	cur = cmp.Draw(buf, buf.Bounds())
	require.Nil(t, cur)
	require.Equal(t, 3, sc.drawCache.Misses())
	require.Equal(t, 3, sc.drawCache.Hits())
	require.NotContains(t, buf.Render(), "Status updated")

	// Empty draw area is a no-op and does not panic.
	require.Nil(t, cmp.Draw(buf, image.Rectangle{}))
	require.Equal(t, 3, sc.drawCache.Misses())
	require.Equal(t, 3, sc.drawCache.Hits())
}
