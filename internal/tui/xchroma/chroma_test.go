package xchroma

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/stretchr/testify/require"
)

func TestMatchLexerMemoization(t *testing.T) {
	l1 := MatchLexer("example.go")
	require.NotNil(t, l1)

	l2 := MatchLexer("example.go")
	require.Equal(t, l1, l2)

	unknown1 := MatchLexer("no_known_extension_xyz123")
	require.Nil(t, unknown1)
	unknown2 := MatchLexer("no_known_extension_xyz123")
	require.Nil(t, unknown2)

	// Verify concurrent access is race-free.
	var wg sync.WaitGroup
	files := []string{"a.go", "b.py", "c.ts", "unknown.xyz123"}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, f := range files {
				_ = MatchLexer(f)
			}
		}()
	}
	wg.Wait()
}

func TestFormatterBasicAndProcessValue(t *testing.T) {
	l := MatchLexer("main.go")
	require.NotNil(t, l)

	style := chroma.MustNewStyle("test", chroma.StyleEntries{
		chroma.Keyword:       "#ff0000 bold",
		chroma.CommentSingle: "#00ff00 italic",
		chroma.LiteralString: "#0000ff underline",
	})

	src := "package main\n// comment line\n\tvar s = \"hi\"\n"
	it, err := l.Tokenise(nil, src)
	require.NoError(t, err)

	var buf bytes.Buffer
	f := Formatter(lipgloss.Color("#112233"), func(v string) string {
		return strings.ReplaceAll(v, "\t", "    ")
	})
	require.NoError(t, f.Format(&buf, style, it))

	out := buf.String()
	require.Contains(t, out, "package")
	require.Contains(t, out, "// comment line")
	require.Contains(t, out, "    ")
	require.NotContains(t, out, "\t")
}

func TestFormatterMultilineTokenPreservesIndentation(t *testing.T) {
	style := chroma.MustNewStyle("test-multiline", chroma.StyleEntries{
		chroma.CommentMultiline: "#888888",
	})

	tokens := []chroma.Token{
		{Type: chroma.CommentMultiline, Value: "/* long first line of comment\nshort */"},
	}
	idx := 0
	it := func() chroma.Token {
		if idx >= len(tokens) {
			return chroma.EOF
		}
		tok := tokens[idx]
		idx++
		return tok
	}

	var buf bytes.Buffer
	f := Formatter(lipgloss.Color("#222222"), nil)
	require.NoError(t, f.Format(&buf, style, it))

	lines := strings.Split(buf.String(), "\n")
	require.Len(t, lines, 2)
	// Line 2 should NOT be padded out to the width of line 1.
	require.Less(t, lipgloss.Width(lines[1]), lipgloss.Width(lines[0]))
	require.Equal(t, len("short */"), lipgloss.Width(lines[1]))
}
