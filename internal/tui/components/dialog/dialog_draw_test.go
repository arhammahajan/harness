package dialog

import (
	"image"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/llm/models"
	"github.com/arhammahajan/harness/internal/llm/tools"
	"github.com/arhammahajan/harness/internal/permission"
	"github.com/arhammahajan/harness/internal/session"
	"github.com/arhammahajan/harness/internal/tui/layout"
)

type mockCompletionProvider struct {
	items []CompletionItemI
}

func (m *mockCompletionProvider) GetId() string { return "mock" }
func (m *mockCompletionProvider) GetEntry() CompletionItemI {
	return NewCompletionItem(CompletionItem{Title: "Files", Value: "files"})
}
func (m *mockCompletionProvider) GetChildEntries(query string) ([]CompletionItemI, error) {
	if query == "" {
		return m.items, nil
	}
	var filtered []CompletionItemI
	for _, it := range m.items {
		if strings.Contains(strings.ToLower(it.GetValue()), strings.ToLower(query)) {
			filtered = append(filtered, it)
		}
	}
	return filtered, nil
}

func screenArea() uv.Rectangle {
	return image.Rect(0, 0, 120, 40)
}

func assertBufferContains(t *testing.T, scr uv.ScreenBuffer, needle string) {
	t.Helper()
	rendered := scr.Render()
	if !strings.Contains(rendered, needle) {
		t.Fatalf("expected UV screen buffer to contain %q, got:\n%s", needle, rendered)
	}
}

func TestDialogsImplementDrawable(t *testing.T) {
	var _ layout.Drawable = NewQuitCmp()
	var _ layout.Drawable = NewHelpCmp()
	var _ layout.Drawable = NewThemeDialogCmp()
	var _ layout.Drawable = NewSessionDialogCmp()
	var _ layout.Drawable = NewModelDialogCmp()
	var _ layout.Drawable = NewCommandDialogCmp()
	var _ layout.Drawable = NewInitDialogCmp()
	var _ layout.Drawable = NewMultiArgumentsDialogCmp("cmd", "body", []string{"FOO"})
	var _ layout.Drawable = NewPermissionDialogCmp()
	var _ layout.Drawable = NewCompletionDialogCmp(&mockCompletionProvider{})
	if fp := NewFilepickerCmp(nil); fp != nil {
		var _ layout.Drawable = fp
	}
}

func TestQuitDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewQuitCmp()
	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for QuitDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "Are you sure you want to quit?")

	// Toggle selection with tab and verify update handles tea.KeyPressMsg.
	m, _ := d.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	d = m.(QuitDialog)
	if d.View().Content == "" {
		t.Fatalf("expected non-empty View content")
	}
}

func TestHelpDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewHelpCmp()
	d.SetBindings([]key.Binding{
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "commands")),
	})
	m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	d = m.(HelpCmp)

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for HelpCmp, got %+v", cur)
	}
	assertBufferContains(t, scr, "Keyboard Shortcuts")
	assertBufferContains(t, scr, "commands")
}

func TestThemeDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewThemeDialogCmp()
	d.Init()
	m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	d = m.(ThemeDialog)

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for ThemeDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "Select Theme")
}

func TestSessionDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewSessionDialogCmp()
	d.SetSessions([]session.Session{
		{ID: "s1", Title: "Refactor TUI"},
		{ID: "s2", Title: "Fix tests"},
	})
	d.SetSelectedSession("s2")
	m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	d = m.(SessionDialog)

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for SessionDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "Switch Session")
	assertBufferContains(t, scr, "Refactor TUI")
	assertBufferContains(t, scr, "Fix tests")
}

func TestModelDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	cmp := &modelDialogCmp{
		provider: models.ProviderAnthropic,
		models: []models.Model{
			{ID: models.Claude37Sonnet, Name: "Claude 3.7 Sonnet", Provider: models.ProviderAnthropic},
		},
		selectedIdx: 0,
	}
	var d ModelDialog = cmp

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for ModelDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "Select Anthropic Model")
	assertBufferContains(t, scr, "Claude 3.7 Sonnet")

	// Also verify empty/uninitialized ModelDialog doesn't panic.
	scrEmpty := uv.NewScreenBuffer(120, 40)
	empty := NewModelDialogCmp()
	empty.Draw(scrEmpty, screenArea())
	assertBufferContains(t, scrEmpty, "Select Model")
}

func TestCommandDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewCommandDialogCmp()
	d.SetCommands([]Command{
		{ID: "init", Title: "Initialize Project", Description: "Create Harness.md"},
		{ID: "compact", Title: "Compact Session", Description: "Summarize conversation"},
	})
	m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	d = m.(CommandDialog)

	// Navigate down via KeyPressMsg (SimpleList v2 integration).
	m, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	d = m.(CommandDialog)

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for CommandDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "Commands")
	assertBufferContains(t, scr, "Initialize Project")
	assertBufferContains(t, scr, "Compact Session")
}

func TestInitDialogDraw(t *testing.T) {
	scr := uv.NewScreenBuffer(120, 40)
	d := NewInitDialogCmp()
	d.SetSize(120, 40)

	cur := d.Draw(scr, screenArea())
	if cur != nil {
		t.Fatalf("expected nil cursor for InitDialogCmp, got %+v", cur)
	}
	assertBufferContains(t, scr, "Initialize Project")
	assertBufferContains(t, scr, "Yes")
	assertBufferContains(t, scr, "No")
}

func TestMultiArgumentsDialogDrawAndCursor(t *testing.T) {
	area := screenArea()
	scr := uv.NewScreenBuffer(120, 40)
	d := NewMultiArgumentsDialogCmp("custom:deploy", "deploy $ENV $TAG", []string{"ENV", "TAG"})
	d.SetSize(120, 40)

	cur1 := d.Draw(scr, area)
	if cur1 == nil {
		t.Fatalf("expected non-nil hardware cursor for focused MultiArgumentsDialogCmp")
	}
	if !image.Pt(cur1.X, cur1.Y).In(area) {
		t.Fatalf("expected cursor %+v to be within screen area %v", cur1, area)
	}
	assertBufferContains(t, scr, "Command Arguments")
	assertBufferContains(t, scr, "ENV:")
	assertBufferContains(t, scr, "TAG:")

	// Typing characters should advance hardware cursor X.
	m, _ := d.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	d = m.(MultiArgumentsDialogCmp)
	m, _ = d.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	d = m.(MultiArgumentsDialogCmp)

	scr2 := uv.NewScreenBuffer(120, 40)
	cur2 := d.Draw(scr2, area)
	if cur2 == nil {
		t.Fatalf("expected non-nil hardware cursor after typing")
	}
	if cur2.X != cur1.X+2 || cur2.Y != cur1.Y {
		t.Fatalf("expected cursor to advance by 2 columns from (%d,%d), got (%d,%d)", cur1.X, cur1.Y, cur2.X, cur2.Y)
	}

	// Pressing Tab should move focus to TAG input and shift hardware cursor Y down.
	m, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	d = m.(MultiArgumentsDialogCmp)

	scr3 := uv.NewScreenBuffer(120, 40)
	cur3 := d.Draw(scr3, area)
	if cur3 == nil {
		t.Fatalf("expected non-nil hardware cursor after Tab")
	}
	if cur3.Y <= cur2.Y {
		t.Fatalf("expected cursor Y to move down on Tab from %d, got %d", cur2.Y, cur3.Y)
	}
	if cur3.X != cur1.X {
		t.Fatalf("expected cursor X on empty second input to equal initial X %d, got %d", cur1.X, cur3.X)
	}
}

func TestPermissionDialogDraw(t *testing.T) {
	area := screenArea()
	cases := []struct {
		name string
		req  permission.PermissionRequest
		want string
	}{
		{
			name: "bash",
			req: permission.PermissionRequest{
				ID:       "p-bash",
				ToolName: tools.BashToolName,
				Path:     "/tmp",
				Params:   tools.BashPermissionsParams{Command: "echo hello"},
			},
			want: "Permission Required",
		},
		{
			name: "edit",
			req: permission.PermissionRequest{
				ID:       "p-edit",
				ToolName: tools.EditToolName,
				Path:     "/tmp/foo.go",
				Params: tools.EditPermissionsParams{
					FilePath: "/tmp/foo.go",
					Diff:     "--- a/foo.go\n+++ b/foo.go\n@@ -1 +1 @@\n-old\n+new\n",
				},
			},
			want: "Allow (a)",
		},
		{
			name: "default",
			req: permission.PermissionRequest{
				ID:          "p-default",
				ToolName:    "custom",
				Path:        "/tmp",
				Description: "Run custom action",
			},
			want: "Deny (d)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scr := uv.NewScreenBuffer(120, 40)
			d := NewPermissionDialogCmp()
			m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			d = m.(PermissionDialogCmp)
			d.SetPermissions(tc.req)

			cur := d.Draw(scr, area)
			if cur != nil {
				t.Fatalf("expected nil cursor for PermissionDialogCmp, got %+v", cur)
			}
			assertBufferContains(t, scr, tc.want)
		})
	}
}

func TestCompletionDialogDraw(t *testing.T) {
	area := screenArea()
	scr := uv.NewScreenBuffer(120, 40)
	provider := &mockCompletionProvider{
		items: []CompletionItemI{
			NewCompletionItem(CompletionItem{Title: "main.go", Value: "main.go"}),
			NewCompletionItem(CompletionItem{Title: "README.md", Value: "README.md"}),
		},
	}
	d := NewCompletionDialogCmp(provider)
	d.SetWidth(60)

	cur := d.Draw(scr, area)
	if cur != nil {
		t.Fatalf("expected nil cursor for CompletionDialog, got %+v", cur)
	}
	assertBufferContains(t, scr, "main.go")
	assertBufferContains(t, scr, "README.md")

	// Activate pseudo-search with '@' and filter with 'r'.
	m, _ := d.Update(tea.KeyPressMsg{Code: '@', Text: "@"})
	d = m.(CompletionDialog)
	m, _ = d.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	d = m.(CompletionDialog)

	scrFiltered := uv.NewScreenBuffer(120, 40)
	d.Draw(scrFiltered, area)
	assertBufferContains(t, scrFiltered, "README.md")
}

func TestFilepickerDialogDrawAndCursor(t *testing.T) {
	d := NewFilepickerCmp(nil)
	if d == nil {
		t.Skip("user home directory unavailable")
	}
	area := screenArea()
	m, _ := d.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	d = m.(FilepickerCmp)

	scr := uv.NewScreenBuffer(120, 40)
	if cur := d.Draw(scr, area); cur != nil {
		t.Fatalf("expected nil cursor when cwd input is blurred, got %+v", cur)
	}
	assertBufferContains(t, scr, "Press i to start typing path")

	// Focus CWD textinput with 'i' and verify hardware cursor is returned inside area.
	m, _ = d.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	d = m.(FilepickerCmp)
	if !d.IsCWDFocused() {
		t.Fatalf("expected CWD textinput to be focused after pressing 'i'")
	}

	scrFocused := uv.NewScreenBuffer(120, 40)
	cur := d.Draw(scrFocused, area)
	if cur == nil {
		t.Fatalf("expected non-nil hardware cursor when CWD textinput is focused")
	}
	if !image.Pt(cur.X, cur.Y).In(area) {
		t.Fatalf("expected filepicker cursor %+v to be inside screen area %v", cur, area)
	}
	assertBufferContains(t, scrFocused, "Press esc to exit typing path")
}
