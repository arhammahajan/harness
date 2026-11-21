package markdown

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"charm.land/glamour/v2"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/stretchr/testify/require"
)

// freshRender renders content as a single document using the shared
// renderer (properly locked) and applies the same trailing-newline trim
// that StreamingMarkdown.Render does.
func freshRender(t *testing.T, content string, width int) string {
	t.Helper()
	r := styles.GetMarkdownRenderer(width)
	mu := styles.LockMarkdownRenderer(r)
	mu.Lock()
	out, err := r.Render(content)
	mu.Unlock()
	require.NoError(t, err)
	return strings.TrimSuffix(out, "\n")
}

// stripANSI removes all ANSI CSI escape sequences from s.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				if c >= 0x40 && c <= 0x7e {
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// nonBlankLines returns visible non-blank lines with trailing spaces trimmed.
func nonBlankLines(s string) []string {
	clean := stripANSI(s)
	out := make([]string, 0)
	for _, l := range strings.Split(clean, "\n") {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// assertVisuallyExactRender asserts that got matches want in exact visible
// layout (every space, line width, and blank line identical after ANSI strip)
// AND every non-blank line is byte-for-byte identical including all ANSI codes.
func assertVisuallyExactRender(t *testing.T, want, got string) {
	t.Helper()
	require.Equal(t, stripANSI(want), stripANSI(got),
		"visible text, spacing, and line widths must match exactly")

	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	require.Equal(t, len(wantLines), len(gotLines), "line count must match")
	for i := range wantLines {
		if strings.TrimSpace(stripANSI(wantLines[i])) == "" {
			continue
		}
		require.Equalf(t, wantLines[i], gotLines[i], "non-blank line %d must match byte-for-byte (including ANSI)", i)
	}
}

// progressivePrefixes splits doc into n monotonically growing byte prefixes,
// ending with the full document.
func progressivePrefixes(doc string, n int) []string {
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		size := len(doc) * i / n
		if i == n {
			size = len(doc)
		}
		if size > 0 {
			out = append(out, doc[:size])
		}
	}
	return out
}

// longProse returns n lines of plain prose joined by single newlines
// (no blank lines anywhere).
func longProse(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("Let me reconsider step %d: the constraint holds for every branch here.", i)
	}
	return strings.Join(lines, "\n")
}

func TestMarkdownSentinel_RendersToNothing(t *testing.T) {
	t.Parallel()

	for _, width := range []int{20, 40, 80, 120} {
		r := styles.GetMarkdownRenderer(width)
		mu := styles.LockMarkdownRenderer(r)
		mu.Lock()
		out, err := r.Render(markdownSentinel)
		mu.Unlock()
		require.NoError(t, err)
		require.Empty(t, out, "sentinel must render to empty string at width %d", width)
	}
}

func TestFindSafeMarkdownBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "empty",
			content: "",
			want:    -1,
		},
		{
			name:    "single line",
			content: "Just a single paragraph",
			want:    -1,
		},
		{
			name:    "two paragraphs",
			content: "First paragraph.\n\nSecond paragraph.",
			want:    len("First paragraph.\n\n"),
		},
		{
			name:    "blank line with spaces and tabs",
			content: "First.\n  \t \nSecond.",
			want:    len("First.\n  \t \n"),
		},
		{
			name:    "three paragraphs picks latest",
			content: "First.\n\nSecond.\n\nThird.",
			want:    len("First.\n\nSecond.\n\n"),
		},
		{
			name:    "open fence at end accepts earlier boundary",
			content: "Para.\n\n```go\nfoo()\n",
			want:    len("Para.\n\n"),
		},
		{
			name:    "inside open fence rejects inner blank line",
			content: "Para.\n\n```go\nfoo()\n\nbar()\n",
			want:    len("Para.\n\n"),
		},
		{
			name:    "closed backtick fence followed by paragraph",
			content: "Para1.\n\n```go\nfoo()\n```\n\nPara2.",
			want:    len("Para1.\n\n```go\nfoo()\n```\n\n"),
		},
		{
			name:    "closed tilde fence followed by paragraph",
			content: "Para1.\n\n~~~go\nfoo()\n~~~\n\nPara2.",
			want:    len("Para1.\n\n~~~go\nfoo()\n~~~\n\n"),
		},
		{
			name:    "html and list inside closed fence do not trigger hazards",
			content: "Para1.\n\n```\n<div>\n[ref]: http://example.com\n- item\n```\n\nPara2.",
			want:    len("Para1.\n\n```\n<div>\n[ref]: http://example.com\n- item\n```\n\n"),
		},
		{
			name:    "open list at end accepts boundary before list",
			content: "Para.\n\n- one\n- two\n",
			want:    len("Para.\n\n"),
		},
		{
			name:    "list interior without blank line",
			content: "- one\n- two\n",
			want:    -1,
		},
		{
			name:    "closed list immediately before blank line rejected",
			content: "- one\n- two\n\nPara.",
			want:    -1,
		},
		{
			name:    "closed list followed by non-indented paragraph allows later boundary",
			content: "- one\n- two\n\nClosed paragraph.\n\nNext.",
			want:    len("- one\n- two\n\nClosed paragraph.\n\n"),
		},
		{
			name:    "loose list continuation indented paragraph rejected",
			content: "Intro.\n\n- one\n\n  indented continuation\n\n- two",
			want:    len("Intro.\n\n"),
		},
		{
			name:    "ordered list marker paren style",
			content: "Intro.\n\n1) first\n2) second\n\nNext.",
			want:    len("Intro.\n\n"),
		},
		{
			name:    "table at end accepts boundary before table",
			content: "Para.\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n",
			want:    len("Para.\n\n"),
		},
		{
			name:    "table interior blank line rejected",
			content: "| a | b |\n| --- | --- |\n\n| 1 | 2 |\n",
			want:    -1,
		},
		{
			name:    "block quote at end accepts boundary before quote",
			content: "Para.\n\n> quoted\n> still quoted\n",
			want:    len("Para.\n\n"),
		},
		{
			name:    "block quote ending before blank line rejected",
			content: "> quoted\n\nNext.",
			want:    -1,
		},
		{
			name:    "setext underline equals pending rejected",
			content: "Heading\n\n=====\n",
			want:    -1,
		},
		{
			name:    "setext underline dashes pending rejected",
			content: "Heading\n\n-----\n",
			want:    -1,
		},
		{
			name:    "setext underline as last line of prefix rejected",
			content: "Heading\n=====\n\nNext.",
			want:    -1,
		},
		{
			name:    "indented code 4 spaces rejected",
			content: "Para.\n\n    code line\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "indented code tab rejected",
			content: "Para.\n\n\tcode line\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block div rejected",
			content: "Para.\n\n<div>\nhi\n</div>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block comment rejected",
			content: "Para.\n\n<!-- comment -->\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block script rejected",
			content: "Para.\n\n<script>\nvar x = 1;\n</script>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block processing instruction rejected",
			content: "Para.\n\n<?xml version=\"1.0\"?>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block cdata rejected",
			content: "Para.\n\n<![CDATA[raw]]>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block declaration rejected",
			content: "Para.\n\n<!DOCTYPE html>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "html block custom tag type 7 rejected",
			content: "Para.\n\n<custom-tag>\nbody\n</custom-tag>\n\nNext.",
			want:    len("Para.\n\n"),
		},
		{
			name:    "non-html angle bracket tokens allowed",
			content: "<3 heart\n\n<- arrow\n\nNext.",
			want:    len("<3 heart\n\n<- arrow\n\n"),
		},
		{
			name:    "reference link definition rejected",
			content: "Para.\n\n[ref]: https://example.com\n\nUse [ref].",
			want:    len("Para.\n\n"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := findSafeMarkdownBoundary(c.content)
			require.Equalf(t, c.want, got, "findSafeMarkdownBoundary(%q)", c.content)
			if got > 0 {
				require.LessOrEqual(t, got, len(c.content))
				require.Equal(t, byte('\n'), c.content[got-1])
			}
		})
	}
}

func TestStreamingMarkdown_StreamingEquivalence(t *testing.T) {
	t.Parallel()

	const width = 80

	scenarios := []struct {
		name            string
		doc             string
		exactByteMatch  bool
	}{
		{
			name:           "paragraphs",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"First paragraph with **bold** and *italic* text.",
				"",
				"Second paragraph with `inline code` and a [link](https://example.com).",
				"",
				"Third paragraph wraps cleanly across multiple words to check alignment.",
				"",
				"Fourth paragraph ends the document.",
			}, "\n"),
		},
		{
			name:           "headings",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"# Heading One",
				"",
				"Intro under heading one.",
				"",
				"## Heading Two",
				"",
				"Body under heading two.",
				"",
				"### Heading Three",
				"",
				"Closing text.",
			}, "\n"),
		},
		{
			name:           "fenced-code-blocks",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"```go",
				"package main",
				"",
				"import \"fmt\"",
				"",
				"func main() {",
				"\tfmt.Println(\"hello\")",
				"}",
				"```",
				"",
				"Middle paragraph.",
				"",
				"~~~python",
				"def greet(name):",
				"    return f\"hi {name}\"",
				"~~~",
				"",
				"Outro paragraph.",
			}, "\n"),
		},
		{
			name:           "tight-lists",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"Before list.",
				"",
				"- alpha",
				"- beta",
				"- gamma",
				"",
				"Between lists.",
				"",
				"1. first",
				"2. second",
				"3. third",
				"",
				"After lists.",
			}, "\n"),
		},
		{
			name:           "loose-list",
			exactByteMatch: false, // glamour adjacency margin may differ across seam
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"- item one",
				"",
				"  continuation paragraph for item one",
				"",
				"- item two",
				"",
				"Trailing paragraph after loose list.",
			}, "\n"),
		},
		{
			name:           "blockquotes",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"> First quoted line.",
				"> Second quoted line.",
				"",
				"After quote paragraph.",
			}, "\n"),
		},
		{
			name:           "tables",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"| Name  | Value |",
				"| ----- | ----- |",
				"| alpha | 1     |",
				"| beta  | 2     |",
				"",
				"After table paragraph.",
			}, "\n"),
		},
		{
			name:           "setext-headers",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"Setext H1",
				"=========",
				"",
				"Body text.",
				"",
				"Setext H2",
				"---------",
				"",
				"Closing text.",
			}, "\n"),
		},
		{
			name:           "html-blocks",
			exactByteMatch: false,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"<div>",
				"raw html content",
				"</div>",
				"",
				"Trailing paragraph after HTML.",
			}, "\n"),
		},
		{
			name:           "reference-links",
			exactByteMatch: false,
			doc: strings.Join([]string{
				"Intro paragraph.",
				"",
				"[docs]: https://example.com/docs",
				"",
				"Read the [project docs][docs] for details.",
			}, "\n"),
		},
		{
			name:           "mixed-document",
			exactByteMatch: true,
			doc: strings.Join([]string{
				"# Overview",
				"",
				"This is an overview paragraph.",
				"",
				"```go",
				"func add(a, b int) int { return a + b }",
				"```",
				"",
				"> Important callout.",
				"",
				"- bullet one",
				"- bullet two",
				"",
				"| Key | Val |",
				"| --- | --- |",
				"| k1  | v1  |",
				"",
				"Final summary paragraph.",
			}, "\n"),
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			r := styles.GetMarkdownRenderer(width)
			fresh := freshRender(t, sc.doc, width)

			// Chunk-by-chunk streaming.
			t.Run("chunk-by-chunk", func(t *testing.T) {
				var sm StreamingMarkdown
				var last string
				for _, p := range progressivePrefixes(sc.doc, 20) {
					last = sm.Render(p, width, r)
					require.NotEmpty(t, last)
				}
				if sc.exactByteMatch {
					assertVisuallyExactRender(t, fresh, last)
				} else {
					require.Equal(t, nonBlankLines(fresh), nonBlankLines(last))
				}
			})

			// Byte-by-byte streaming.
			t.Run("byte-by-byte", func(t *testing.T) {
				var sm StreamingMarkdown
				var last string
				for i := 1; i <= len(sc.doc); i++ {
					last = sm.Render(sc.doc[:i], width, r)
				}
				if sc.exactByteMatch {
					assertVisuallyExactRender(t, fresh, last)
				} else {
					require.Equal(t, nonBlankLines(fresh), nonBlankLines(last))
				}
			})
		})
	}
}

func TestStreamingMarkdown_PrefixPromotionAndHazards(t *testing.T) {
	t.Parallel()

	const width = 80
	r := styles.GetMarkdownRenderer(width)

	t.Run("advances across blank lines", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		// Cold render does not seed prefix cache.
		p1 := "Para one.\n\nPara two."
		_ = sm.Render(p1, width, r)
		require.Equal(t, 0, sm.StablePrefixLen(), "cold render should not seed prefix")

		// Growing content seeds the prefix at the blank line.
		p2 := p1 + " More."
		_ = sm.Render(p2, width, r)
		require.Equal(t, len("Para one.\n\n"), sm.StablePrefixLen())

		// Adding another paragraph advances the boundary further.
		p3 := p2 + "\n\nPara three."
		_ = sm.Render(p3, width, r)
		require.Equal(t, len(p2+"\n\n"), sm.StablePrefixLen())
	})

	t.Run("does not advance inside open code fence", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Intro.\n\n```go\nline1()\n\nline2()\n\nline3()\n"
		fenceStart := strings.Index(doc, "```go")
		for _, p := range progressivePrefixes(doc, 15) {
			_ = sm.Render(p, width, r)
			require.LessOrEqual(t, sm.StablePrefixLen(), fenceStart)
		}
		// Closing the fence and adding a blank line allows promotion past the fence.
		closed := doc + "```\n\nAfter code."
		out := sm.Render(closed, width, r)
		require.Greater(t, sm.StablePrefixLen(), fenceStart)
		assertVisuallyExactRender(t, freshRender(t, closed, width), out)
	})

	t.Run("does not advance past HTML block", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Intro.\n\n<div>\nhello\n</div>\n\nTrailing paragraph."
		hazard := strings.Index(doc, "<div>")
		for _, p := range progressivePrefixes(doc, 20) {
			_ = sm.Render(p, width, r)
			require.LessOrEqual(t, sm.StablePrefixLen(), hazard)
		}
	})

	t.Run("does not advance past link-ref definition", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Intro.\n\n[ref]: https://example.com\n\nSee [example][ref] here."
		hazard := strings.Index(doc, "[ref]:")
		for _, p := range progressivePrefixes(doc, 20) {
			_ = sm.Render(p, width, r)
			require.LessOrEqual(t, sm.StablePrefixLen(), hazard)
		}
	})

	t.Run("does not advance inside loose list continuation", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Intro.\n\n- item one\n\n  continuation paragraph\n\n- item two"
		hazard := strings.Index(doc, "- item one")
		for _, p := range progressivePrefixes(doc, 20) {
			_ = sm.Render(p, width, r)
			require.LessOrEqual(t, sm.StablePrefixLen(), hazard)
		}
	})

	t.Run("advances after closed list followed by non-indented paragraph", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Intro.\n\n- one\n- two\n\nClosed paragraph.\n\nNext paragraph."
		for _, p := range progressivePrefixes(doc, 20) {
			_ = sm.Render(p, width, r)
		}
		wantBoundary := strings.Index(doc, "Next paragraph.")
		require.Equal(t, wantBoundary, sm.StablePrefixLen())
	})

	t.Run("does not advance before setext underline", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		doc := "Heading\n\n=====\n"
		for i := 1; i <= len(doc); i++ {
			_ = sm.Render(doc[:i], width, r)
		}
		// Never promoted across the setext underline boundary.
		// Note: while "Heading\n\n" alone had not seen "=", once "=" arrives
		// the check rejects; verify with direct incremental call when "=" is present.
		var sm2 StreamingMarkdown
		_ = sm2.Render("Heading\n", width, r)
		_ = sm2.Render("Heading\n\n=====\n", width, r)
		require.Equal(t, 0, sm2.StablePrefixLen())
	})
}

func TestStreamingMarkdown_RelaxedBoundary(t *testing.T) {
	t.Parallel()

	const width = 80
	r := styles.GetMarkdownRenderer(width)

	t.Run("triggers for >2KB unbroken prose with newlines", func(t *testing.T) {
		t.Parallel()
		doc := longProse(200)
		require.Greater(t, len(doc), relaxBoundaryAfter*2)
		require.Equal(t, -1, findSafeMarkdownBoundary(doc))

		var sm StreamingMarkdown
		var last string
		for _, p := range progressivePrefixes(doc, 30) {
			last = sm.Render(p, width, r)
			require.NotEmpty(t, last)
			require.LessOrEqual(t, sm.StablePrefixLen(), len(p))
			require.True(t, strings.HasPrefix(p, p[:sm.StablePrefixLen()]))
		}
		require.Greater(t, sm.StablePrefixLen(), 0, "relaxed boundary must advance on >2KB prose")

		visible := stripANSI(last)
		require.Contains(t, visible, "step 0:")
		require.Contains(t, visible, "step 199:")
	})

	t.Run("does not trigger when tail <= 2KB", func(t *testing.T) {
		t.Parallel()
		doc := longProse(10)
		require.Less(t, len(doc), relaxBoundaryAfter)

		var sm StreamingMarkdown
		for _, p := range progressivePrefixes(doc, 10) {
			_ = sm.Render(p, width, r)
		}
		require.Equal(t, 0, sm.StablePrefixLen())
	})

	t.Run("respects hazards even past 2KB", func(t *testing.T) {
		t.Parallel()
		cases := map[string]string{
			"open code fence": "```go\n" + longProse(100),
			"long table": func() string {
				rows := make([]string, 200)
				rows[0] = "| a | b | c |"
				rows[1] = "| - | - | - |"
				for i := 2; i < len(rows); i++ {
					rows[i] = fmt.Sprintf("| %d | %d | %d |", i, i+1, i+2)
				}
				return strings.Join(rows, "\n")
			}(),
			"html opener": "<div>\n" + longProse(100),
			"link ref":    "[ref]: https://example.com\n" + longProse(100),
		}
		for name, doc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				require.Greater(t, len(doc), relaxBoundaryAfter)
				var sm StreamingMarkdown
				for _, p := range progressivePrefixes(doc, 20) {
					require.NotEmpty(t, sm.Render(p, width, r))
				}
				require.Equal(t, 0, sm.StablePrefixLen())
			})
		}
	})

	t.Run("no newlines at all stays on full render path", func(t *testing.T) {
		t.Parallel()
		doc := strings.Repeat("word ", 700) // >3KB, zero newlines
		require.Greater(t, len(doc), relaxBoundaryAfter)
		var sm StreamingMarkdown
		_ = sm.Render(doc[:1000], width, r)
		out := sm.Render(doc, width, r)
		require.Equal(t, 0, sm.StablePrefixLen())
		require.Equal(t, freshRender(t, doc, width), out)
	})
}

func TestStreamingMarkdown_CacheInvalidationAndMemoization(t *testing.T) {
	t.Parallel()

	r80 := styles.GetMarkdownRenderer(80)
	r40 := styles.GetMarkdownRenderer(40)

	t.Run("repeat render is memoized", func(t *testing.T) {
		t.Parallel()
		doc := "# Title\n\nFirst paragraph.\n\nSecond paragraph."
		var sm StreamingMarkdown
		first := sm.Render(doc, 80, r80)
		lines := sm.LastLines()

		// Passing a different-width renderer with the same width key proves memo hit.
		again := sm.Render(doc, 80, r40)
		require.Equal(t, first, again)
		require.Equal(t, lines, sm.LastLines())
	})

	t.Run("width change resets cache", func(t *testing.T) {
		t.Parallel()
		doc := "First paragraph with enough words to wrap differently at forty columns versus eighty columns.\n\nSecond paragraph."
		var sm StreamingMarkdown
		for _, p := range progressivePrefixes(doc, 8) {
			_ = sm.Render(p, 80, r80)
		}
		require.Greater(t, sm.StablePrefixLen(), 0)

		out40 := sm.Render(doc, 40, r40)
		require.Equal(t, 0, sm.StablePrefixLen(), "width change resets cache and does not re-seed on cold render")
		require.Equal(t, freshRender(t, doc, 40), out40)
	})

	t.Run("non-prefix rewrite resets cache", func(t *testing.T) {
		t.Parallel()
		doc := "Para one.\n\nPara two.\n\nPara three."
		var sm StreamingMarkdown
		for _, p := range progressivePrefixes(doc, 6) {
			_ = sm.Render(p, 80, r80)
		}
		require.Greater(t, sm.StablePrefixLen(), 0)

		other := "Completely different opening.\n\nSecond paragraph."
		out := sm.Render(other, 80, r80)
		require.Equal(t, 0, sm.StablePrefixLen())
		require.Equal(t, freshRender(t, other, 80), out)
	})

	t.Run("reset clears all state", func(t *testing.T) {
		t.Parallel()
		doc := "Para one.\n\nPara two."
		var sm StreamingMarkdown
		_ = sm.Render("Para one.\n\nPa", 80, r80)
		_ = sm.Render(doc, 80, r80)
		require.Greater(t, sm.StablePrefixLen(), 0)
		require.Greater(t, sm.LastLines(), 0)

		sm.Reset()
		require.Equal(t, 0, sm.StablePrefixLen())
		require.Equal(t, 0, sm.LastLines())
		require.Equal(t, 0, sm.width)
		require.Empty(t, sm.stablePrefixRender)
		require.Empty(t, sm.lastContent)
		require.Empty(t, sm.lastRender)

		out := sm.Render(doc, 80, r80)
		require.Equal(t, freshRender(t, doc, 80), out)
	})

	t.Run("nil renderer falls back gracefully", func(t *testing.T) {
		t.Parallel()
		var sm StreamingMarkdown
		out := sm.Render("line1\nline2", 80, nil)
		require.Equal(t, "line1\nline2", out)
		require.Equal(t, 2, sm.LastLines())
	})
}

func TestStreamingMarkdown_LastLinesMatchesTrimmedOutput(t *testing.T) {
	t.Parallel()

	const width = 80
	r := styles.GetMarkdownRenderer(width)

	docs := map[string]string{
		"empty":                 "",
		"single line":           "Hello world.",
		"blank-line-free prose": longProse(200),
		"paragraphs":            strings.Repeat("A paragraph of reasoning that runs on for a while.\n\n", 40),
		"mixed":                 longProse(40) + "\n\n" + strings.Repeat("- a list item\n", 15) + "\n\n" + longProse(80),
		"fenced":                "```go\nfunc main() {}\n```\n\n" + longProse(80),
		"table":                 "| a | b |\n| - | - |\n| 1 | 2 |\n\n" + longProse(80),
	}

	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var sm StreamingMarkdown
			if doc == "" {
				out := sm.Render("", width, r)
				require.Equal(t, CountLines(trimGlamourMargins(out)), sm.LastLines())
				return
			}
			for i, p := range progressivePrefixes(doc, 40) {
				out := sm.Render(p, width, r)
				require.Equalf(t, CountLines(trimGlamourMargins(out)), sm.LastLines(),
					"step %d (len=%d): LastLines must match CountLines(trimGlamourMargins(out))", i, len(p))
				require.Equalf(t, CountLines(strings.TrimSpace(out)), sm.LastLines(),
					"step %d (len=%d): LastLines must match CountLines(strings.TrimSpace(out))", i, len(p))
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want int
	}{
		{"", 1},
		{"hello", 1},
		{"hello\n", 2},
		{"a\nb", 2},
		{"a\nb\nc", 3},
		{"\n\n\n", 4},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, CountLines(tc.in), "CountLines(%q)", tc.in)
		// Also verify equivalence with strings.Split.
		require.Equalf(t, len(strings.Split(tc.in, "\n")), CountLines(tc.in), "CountLines vs Split(%q)", tc.in)
	}
}

func TestTailLines(t *testing.T) {
	t.Parallel()

	doc := "line1\nline2\nline3\nline4\nline5"
	total := CountLines(doc)
	require.Equal(t, 5, total)

	t.Run("n <= 0 hides everything", func(t *testing.T) {
		tail, hidden := TailLines(doc, 0, total)
		require.Equal(t, "", tail)
		require.Equal(t, 5, hidden)

		tail, hidden = TailLines(doc, -3, total)
		require.Equal(t, "", tail)
		require.Equal(t, 5, hidden)
	})

	t.Run("totalLines <= n returns whole string", func(t *testing.T) {
		tail, hidden := TailLines(doc, 5, total)
		require.Equal(t, doc, tail)
		require.Equal(t, 0, hidden)

		tail, hidden = TailLines(doc, 10, total)
		require.Equal(t, doc, tail)
		require.Equal(t, 0, hidden)
	})

	t.Run("slices last n lines", func(t *testing.T) {
		tail, hidden := TailLines(doc, 1, total)
		require.Equal(t, "line5", tail)
		require.Equal(t, 4, hidden)

		tail, hidden = TailLines(doc, 2, total)
		require.Equal(t, "line4\nline5", tail)
		require.Equal(t, 3, hidden)

		tail, hidden = TailLines(doc, 4, total)
		require.Equal(t, "line2\nline3\nline4\nline5", tail)
		require.Equal(t, 1, hidden)
	})

	t.Run("trailing newline counts as empty final line", func(t *testing.T) {
		s := "a\nb\nc\n"
		tot := CountLines(s) // 4: "a", "b", "c", ""
		require.Equal(t, 4, tot)

		tail, hidden := TailLines(s, 2, tot)
		require.Equal(t, "c\n", tail)
		require.Equal(t, 2, hidden)
	})

	t.Run("overestimated totalLines falls back safely", func(t *testing.T) {
		s := "only\ntwo"
		tail, hidden := TailLines(s, 3, 10)
		require.Equal(t, s, tail)
		require.Equal(t, 0, hidden)
	})
}

func TestStreamingMarkdown_ConcurrentSharedRenderer(t *testing.T) {
	t.Parallel()

	const width = 80
	shared := styles.GetMarkdownRenderer(width)

	docs := []string{
		"# Title A\n\nFirst paragraph.\n\n```go\nfunc a() int { return 1 }\n```\n\nDone A.",
		"# Title B\n\n- item 1\n- item 2\n\n| k | v |\n| - | - |\n| x | y |\n\nDone B.",
		longProse(120),
		"Intro.\n\n> quoted text\n\nEnding paragraph.",
	}

	expected := make([]string, len(docs))
	for i, d := range docs {
		expected[i] = freshRender(t, d, width)
	}

	var wg sync.WaitGroup
	for g := range 12 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx := id % len(docs)
			doc := docs[idx]
			var sm StreamingMarkdown
			var last string
			for _, p := range progressivePrefixes(doc, 15) {
				last = sm.Render(p, width, shared)
				require.NotEmpty(t, last)
			}
			// For relaxed-boundary long prose, visible lines match; for structured docs, visual layout & ANSI styling match.
			if idx == 2 {
				require.Contains(t, stripANSI(last), "step 0:")
				require.Contains(t, stripANSI(last), "step 119:")
			} else {
				assertVisuallyExactRender(t, expected[idx], last)
			}
		}(g)
	}
	wg.Wait()
}

func TestStripTrailingBlankStub(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no newline", " \x1b[m", " \x1b[m"},
		{"ends with newline", "hello\n", "hello\n"},
		{"heading margin stub", " # Title\n \x1b[38;2;205;214;244m\x1b[m", " # Title\n"},
		{"codeblock margin stub", "  code\n \x1b[38;2;205;214;244m \x1b[m\x1b[38;2;205;214;244m\x1b[m", "  code\n"},
		{"visible content on last line kept", "line1\n \x1b[31mhi\x1b[m", "line1\n \x1b[31mhi\x1b[m"},
		{"incomplete escape kept", "line1\n \x1b[38", "line1\n \x1b[38"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, stripTrailingBlankStub(tc.in))
		})
	}
}

// Compile-time check that StreamingMarkdown accepts *glamour.TermRenderer.
var _ func(*StreamingMarkdown, string, int, *glamour.TermRenderer) string = (*StreamingMarkdown).Render
