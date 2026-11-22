package styles

import (
	"sync"
	"testing"

	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/stretchr/testify/require"
)

func TestGetMarkdownRendererMemoizationAndInvalidation(t *testing.T) {
	origTheme := theme.CurrentThemeName()
	origDark := theme.HasDarkBackground()
	t.Cleanup(func() {
		theme.SetHasDarkBackground(origDark)
		_ = theme.SetTheme(origTheme)
	})

	InvalidateMarkdownRendererCache()

	r80a := GetMarkdownRenderer(80)
	require.NotNil(t, r80a)
	r80b := GetMarkdownRenderer(80)
	require.Same(t, r80a, r80b, "expected same renderer instance for identical width")

	r100 := GetMarkdownRenderer(100)
	require.NotNil(t, r100)
	require.NotSame(t, r80a, r100, "expected distinct renderer instances for different widths")

	mu1 := LockMarkdownRenderer(r80a)
	mu2 := LockMarkdownRenderer(r80a)
	require.Same(t, mu1, mu2, "expected stable per-renderer mutex before invalidation")

	InvalidateMarkdownRendererCache()

	r80c := GetMarkdownRenderer(80)
	require.NotSame(t, r80a, r80c, "expected fresh renderer after cache invalidation")

	mu3 := LockMarkdownRenderer(r80c)
	require.NotSame(t, mu1, mu3, "expected fresh mutex for newly minted renderer")
}

func TestMarkdownRendererThemeSwapCodeBlockColoring(t *testing.T) {
	origTheme := theme.CurrentThemeName()
	origDark := theme.HasDarkBackground()
	t.Cleanup(func() {
		theme.SetHasDarkBackground(origDark)
		_ = theme.SetTheme(origTheme)
	})

	theme.SetHasDarkBackground(true)
	md := "```go\npackage main\nfunc hello() string { return \"world\" }\n```\n"

	require.NoError(t, theme.SetTheme("harness"))
	rHarness := GetMarkdownRenderer(80)
	outHarness, err := rHarness.Render(md)
	require.NoError(t, err)
	require.Contains(t, outHarness, "package")
	require.Contains(t, outHarness, "hello")

	// Switching theme should automatically invalidate the cache and produce
	// different ANSI syntax colors for the fenced code block.
	require.NoError(t, theme.SetTheme("dracula"))
	rDracula := GetMarkdownRenderer(80)
	require.NotSame(t, rHarness, rDracula, "SetTheme should invalidate cached renderers")

	outDracula, err := rDracula.Render(md)
	require.NoError(t, err)
	require.Contains(t, outDracula, "package")
	require.NotEqual(t, outHarness, outDracula, "code block highlighting should reflect the new theme colors")

	// Switching dark/light background should also automatically invalidate the cache.
	theme.SetHasDarkBackground(false)
	rDraculaLight := GetMarkdownRenderer(80)
	require.NotSame(t, rDracula, rDraculaLight, "SetHasDarkBackground should invalidate cached renderers")

	outDraculaLight, err := rDraculaLight.Render(md)
	require.NoError(t, err)
	require.NotEqual(t, outDracula, outDraculaLight, "code block highlighting should reflect light background colors")
}

func TestMarkdownRendererConcurrentSafety(t *testing.T) {
	origTheme := theme.CurrentThemeName()
	origDark := theme.HasDarkBackground()
	t.Cleanup(func() {
		theme.SetHasDarkBackground(origDark)
		_ = theme.SetTheme(origTheme)
	})

	md := "# Heading\n\n```go\nfunc f() int { return 42 }\n```\n"
	themes := []string{"harness", "dracula", "gruvbox", "monokai"}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 5 {
				if id%2 == 0 {
					_ = theme.SetTheme(themes[(id+j)%len(themes)])
				}
				r := GetMarkdownRenderer(80)
				mu := LockMarkdownRenderer(r)
				mu.Lock()
				out, err := r.Render(md)
				mu.Unlock()
				require.NoError(t, err)
				require.Contains(t, out, "Heading")
			}
		}(i)
	}
	wg.Wait()
}
