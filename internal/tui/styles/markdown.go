package styles

import (
	"fmt"
	"image/color"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/xchroma"
)

const (
	defaultMargin = 1
	formatterName = "harness"
)

// Code block colors.
//
// Glamour turns a style config's code-block chroma settings into a chroma
// style registered globally under one fixed name, and only the first time:
// renderers built after a theme switch kept highlighting fenced code with
// the colors of the first theme ever rendered. Registering again is no fix,
// because chroma's registry is a plain map that every highlight reads
// without a lock, so writing it while a renderer runs on another goroutine
// is a data race.
//
// So the registry is only written here, at init: it gets a fixed set of
// empty placeholder styles. Each renderer's config names one placeholder
// instead of carrying chroma settings ([codeBlockConfig]), a placeholder
// stands for one distinct set of settings, and the "harness" formatter every
// renderer uses swaps the placeholder for the real style while it formats.
// Placeholders are reused round-robin once all are taken; renderers are
// rebuilt on every theme change, so the live ones always share the current
// settings.
const codeBlockSlots = 32

var (
	// codeBlockSlotOf maps each placeholder style to its slot. It is
	// written only at init.
	codeBlockSlotOf = map[*chroma.Style]int{}

	// codeBlockMu guards the slot assignments below.
	codeBlockMu     sync.Mutex
	codeBlockByKey  = map[string]int{}
	codeBlockKeys   [codeBlockSlots]string
	codeBlockStyles [codeBlockSlots]*chroma.Style
	codeBlockNext   int
)

func init() {
	// NOTE: Glamour does not offer us an option to pass the formatter
	// implementation directly. We need to register and use by name.
	var zero color.Color
	inner := xchroma.Formatter(zero, nil)
	formatters.Register(formatterName, chroma.FormatterFunc(
		func(w io.Writer, style *chroma.Style, it chroma.Iterator) error {
			return inner.Format(w, codeBlockStyle(style), it)
		},
	))
	for i := range codeBlockSlots {
		placeholder := chromastyles.Register(chroma.MustNewStyle(codeBlockSlotName(i), chroma.StyleEntries{}))
		codeBlockSlotOf[placeholder] = i
	}
	theme.OnThemeChange(InvalidateMarkdownRendererCache)
}

// codeBlockSlotName is the registry name of a code block placeholder.
func codeBlockSlotName(slot int) string {
	return fmt.Sprintf("harness-code-block-%d", slot)
}

// codeBlockConfig returns cfg ready to hand to glamour: chroma settings in
// its code block are replaced by the name of the placeholder that stands
// for them. See the comment on [codeBlockSlots].
func codeBlockConfig(cfg ansi.StyleConfig) ansi.StyleConfig {
	if cfg.CodeBlock.Chroma == nil {
		return cfg
	}
	cfg.CodeBlock.Theme = codeBlockSlotName(codeBlockSlot(cfg.CodeBlock.Chroma))
	cfg.CodeBlock.Chroma = nil
	return cfg
}

// codeBlockSlot returns the placeholder slot for the given chroma settings,
// assigning one (and building its style) the first time they are seen.
func codeBlockSlot(c *ansi.Chroma) int {
	entries := ChromaEntries(c)
	var key strings.Builder
	for _, t := range slices.Sorted(maps.Keys(entries)) {
		fmt.Fprintf(&key, "%d=%s;", t, entries[t])
	}

	codeBlockMu.Lock()
	defer codeBlockMu.Unlock()
	if slot, ok := codeBlockByKey[key.String()]; ok {
		return slot
	}
	slot := codeBlockNext
	codeBlockNext = (codeBlockNext + 1) % codeBlockSlots
	if old := codeBlockKeys[slot]; old != "" {
		delete(codeBlockByKey, old)
	}
	codeBlockKeys[slot] = key.String()
	codeBlockStyles[slot] = chroma.MustNewStyle(formatterName, entries)
	codeBlockByKey[key.String()] = slot
	return slot
}

// codeBlockStyle returns the real style for a code block placeholder, or
// style itself when it is not one.
func codeBlockStyle(style *chroma.Style) *chroma.Style {
	slot, ok := codeBlockSlotOf[style]
	if !ok {
		return style
	}
	codeBlockMu.Lock()
	defer codeBlockMu.Unlock()
	if s := codeBlockStyles[slot]; s != nil {
		return s
	}
	return style
}

// ChromaEntries converts a markdown chroma config to chroma style entries,
// the same way glamour does for code blocks. A nil config has no entries.
func ChromaEntries(c *ansi.Chroma) chroma.StyleEntries {
	if c == nil {
		return chroma.StyleEntries{}
	}
	return chroma.StyleEntries{
		chroma.Text:                chromaStyle(c.Text),
		chroma.Error:               chromaStyle(c.Error),
		chroma.Comment:             chromaStyle(c.Comment),
		chroma.CommentPreproc:      chromaStyle(c.CommentPreproc),
		chroma.Keyword:             chromaStyle(c.Keyword),
		chroma.KeywordReserved:     chromaStyle(c.KeywordReserved),
		chroma.KeywordNamespace:    chromaStyle(c.KeywordNamespace),
		chroma.KeywordType:         chromaStyle(c.KeywordType),
		chroma.Operator:            chromaStyle(c.Operator),
		chroma.Punctuation:         chromaStyle(c.Punctuation),
		chroma.Name:                chromaStyle(c.Name),
		chroma.NameBuiltin:         chromaStyle(c.NameBuiltin),
		chroma.NameTag:             chromaStyle(c.NameTag),
		chroma.NameAttribute:       chromaStyle(c.NameAttribute),
		chroma.NameClass:           chromaStyle(c.NameClass),
		chroma.NameConstant:        chromaStyle(c.NameConstant),
		chroma.NameDecorator:       chromaStyle(c.NameDecorator),
		chroma.NameException:       chromaStyle(c.NameException),
		chroma.NameFunction:        chromaStyle(c.NameFunction),
		chroma.NameOther:           chromaStyle(c.NameOther),
		chroma.Literal:             chromaStyle(c.Literal),
		chroma.LiteralNumber:       chromaStyle(c.LiteralNumber),
		chroma.LiteralDate:         chromaStyle(c.LiteralDate),
		chroma.LiteralString:       chromaStyle(c.LiteralString),
		chroma.LiteralStringEscape: chromaStyle(c.LiteralStringEscape),
		chroma.GenericDeleted:      chromaStyle(c.GenericDeleted),
		chroma.GenericEmph:         chromaStyle(c.GenericEmph),
		chroma.GenericInserted:     chromaStyle(c.GenericInserted),
		chroma.GenericStrong:       chromaStyle(c.GenericStrong),
		chroma.GenericSubheading:   chromaStyle(c.GenericSubheading),
		chroma.Background:          chromaStyle(c.Background),
	}
}

func chromaStyle(style ansi.StylePrimitive) string {
	var s strings.Builder

	if style.Color != nil {
		s.WriteString(*style.Color)
	}
	if style.BackgroundColor != nil {
		if s.Len() > 0 {
			s.WriteString(" ")
		}
		s.WriteString("bg:")
		s.WriteString(*style.BackgroundColor)
	}
	if style.Italic != nil && *style.Italic {
		if s.Len() > 0 {
			s.WriteString(" ")
		}
		s.WriteString("italic")
	}
	if style.Bold != nil && *style.Bold {
		if s.Len() > 0 {
			s.WriteString(" ")
		}
		s.WriteString("bold")
	}
	if style.Underline != nil && *style.Underline {
		if s.Len() > 0 {
			s.WriteString(" ")
		}
		s.WriteString("underline")
	}

	return s.String()
}

// mdCacheMu guards mdCache.
//
// Lock ordering: when both mdCacheMu and rendererLocksMu are
// needed (only in InvalidateMarkdownRendererCache), acquire
// mdCacheMu FIRST, then rendererLocksMu. No other call site may
// hold rendererLocksMu while acquiring mdCacheMu.
var (
	mdCacheMu sync.Mutex
	mdCache   = map[int]*glamour.TermRenderer{}
)

// rendererLocksMu guards rendererLocks. We key per-renderer
// mutexes by pointer so the lock granularity matches the
// renderer cache granularity.
//
// Lock ordering: when both mdCacheMu and rendererLocksMu are
// needed (only in InvalidateMarkdownRendererCache), acquire
// mdCacheMu FIRST, then rendererLocksMu.
var (
	rendererLocksMu sync.Mutex
	rendererLocks   = map[*glamour.TermRenderer]*sync.Mutex{}
)

// Helper functions for style pointers
func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func uintPtr(u uint) *uint       { return &u }

// GetMarkdownRenderer returns a glamour [glamour.TermRenderer] configured with
// the current theme and width. Renderers are memoized per width and shared
// across callers; theme or background changes automatically invalidate the
// cache via [InvalidateMarkdownRendererCache].
//
// The returned renderer is NOT safe for concurrent Render calls. Parallel
// callers must serialize via [LockMarkdownRenderer].
func GetMarkdownRenderer(width int) *glamour.TermRenderer {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	if r, ok := mdCache[width]; ok {
		return r
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(codeBlockConfig(generateMarkdownStyleConfig())),
		glamour.WithWordWrap(width),
		glamour.WithChromaFormatter(formatterName),
	)
	mdCache[width] = r
	return r
}

// InvalidateMarkdownRendererCache drops every cached renderer
// AND every per-renderer mutex in a single atomic critical
// section so the two maps cannot disagree mid-toggle. Call this
// whenever the active theme changes so subsequent renderers
// pick up the new ansi.StyleConfig.
//
// Lock ordering: mdCacheMu is acquired first, then rendererLocksMu.
func InvalidateMarkdownRendererCache() {
	mdCacheMu.Lock()
	defer mdCacheMu.Unlock()
	rendererLocksMu.Lock()
	defer rendererLocksMu.Unlock()

	mdCache = map[int]*glamour.TermRenderer{}
	rendererLocks = map[*glamour.TermRenderer]*sync.Mutex{}
}

// LockMarkdownRenderer returns the per-renderer mutex used to
// serialize concurrent Render calls on a shared
// [glamour.TermRenderer] instance. The returned [*sync.Mutex] is
// stable for the lifetime of the renderer (i.e. until
// [InvalidateMarkdownRendererCache] is called).
func LockMarkdownRenderer(r *glamour.TermRenderer) *sync.Mutex {
	rendererLocksMu.Lock()
	defer rendererLocksMu.Unlock()
	if mu, ok := rendererLocks[r]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	rendererLocks[r] = mu
	return mu
}

// generateMarkdownStyleConfig creates an ansi.StyleConfig for markdown
// rendering using adaptive colors from the current theme.
func generateMarkdownStyleConfig() ansi.StyleConfig {
	t := theme.CurrentTheme()

	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockPrefix: "",
				BlockSuffix: "",
				Color:       stringPtr(t.MarkdownText().Hex()),
			},
			Margin: uintPtr(defaultMargin),
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:  stringPtr(t.MarkdownBlockQuote().Hex()),
				Italic: boolPtr(true),
				Prefix: "┃ ",
			},
			Indent:      uintPtr(1),
			IndentToken: stringPtr(" "),
		},
		List: ansi.StyleList{
			LevelIndent: defaultMargin,
			StyleBlock: ansi.StyleBlock{
				IndentToken: stringPtr(" "),
				StylePrimitive: ansi.StylePrimitive{
					Color: stringPtr(t.MarkdownText().Hex()),
				},
			},
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       stringPtr(t.MarkdownHeading().Hex()),
				Bold:        boolPtr(true),
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "# ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "## ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "### ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "#### ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "##### ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "###### ",
				Color:  stringPtr(t.MarkdownHeading().Hex()),
				Bold:   boolPtr(true),
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: boolPtr(true),
			Color:      stringPtr(t.TextMuted().Hex()),
		},
		Emph: ansi.StylePrimitive{
			Color:  stringPtr(t.MarkdownEmph().Hex()),
			Italic: boolPtr(true),
		},
		Strong: ansi.StylePrimitive{
			Bold:  boolPtr(true),
			Color: stringPtr(t.MarkdownStrong().Hex()),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  stringPtr(t.MarkdownHorizontalRule().Hex()),
			Format: "\n─────────────────────────────────────────\n",
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
			Color:       stringPtr(t.MarkdownListItem().Hex()),
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
			Color:       stringPtr(t.MarkdownListEnumeration().Hex()),
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Color:     stringPtr(t.MarkdownLink().Hex()),
			Underline: boolPtr(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: stringPtr(t.MarkdownLinkText().Hex()),
			Bold:  boolPtr(true),
		},
		Image: ansi.StylePrimitive{
			Color:     stringPtr(t.MarkdownImage().Hex()),
			Underline: boolPtr(true),
			Format:    "🖼 {{.text}}",
		},
		ImageText: ansi.StylePrimitive{
			Color:  stringPtr(t.MarkdownImageText().Hex()),
			Format: "{{.text}}",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:  stringPtr(t.MarkdownCode().Hex()),
				Prefix: "",
				Suffix: "",
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Prefix: " ",
					Color:  stringPtr(t.MarkdownCodeBlock().Hex()),
				},
				Margin: uintPtr(defaultMargin),
			},
			Chroma: &ansi.Chroma{
				Text: ansi.StylePrimitive{
					Color: stringPtr(t.MarkdownText().Hex()),
				},
				Error: ansi.StylePrimitive{
					Color: stringPtr(t.Error().Hex()),
				},
				Comment: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxComment().Hex()),
				},
				CommentPreproc: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				Keyword: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				KeywordReserved: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				KeywordNamespace: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				KeywordType: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxType().Hex()),
				},
				Operator: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxOperator().Hex()),
				},
				Punctuation: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxPunctuation().Hex()),
				},
				Name: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxVariable().Hex()),
				},
				NameBuiltin: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxVariable().Hex()),
				},
				NameTag: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				NameAttribute: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxFunction().Hex()),
				},
				NameClass: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxType().Hex()),
				},
				NameConstant: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxVariable().Hex()),
				},
				NameDecorator: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxFunction().Hex()),
				},
				NameFunction: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxFunction().Hex()),
				},
				LiteralNumber: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxNumber().Hex()),
				},
				LiteralString: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxString().Hex()),
				},
				LiteralStringEscape: ansi.StylePrimitive{
					Color: stringPtr(t.SyntaxKeyword().Hex()),
				},
				GenericDeleted: ansi.StylePrimitive{
					Color: stringPtr(t.DiffRemoved().Hex()),
				},
				GenericEmph: ansi.StylePrimitive{
					Color:  stringPtr(t.MarkdownEmph().Hex()),
					Italic: boolPtr(true),
				},
				GenericInserted: ansi.StylePrimitive{
					Color: stringPtr(t.DiffAdded().Hex()),
				},
				GenericStrong: ansi.StylePrimitive{
					Color: stringPtr(t.MarkdownStrong().Hex()),
					Bold:  boolPtr(true),
				},
				GenericSubheading: ansi.StylePrimitive{
					Color: stringPtr(t.MarkdownHeading().Hex()),
				},
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					BlockPrefix: "\n",
					BlockSuffix: "\n",
				},
			},
			CenterSeparator: stringPtr("┼"),
			ColumnSeparator: stringPtr("│"),
			RowSeparator:    stringPtr("─"),
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n ❯ ",
			Color:       stringPtr(t.MarkdownLinkText().Hex()),
		},
		Text: ansi.StylePrimitive{
			Color: stringPtr(t.MarkdownText().Hex()),
		},
		Paragraph: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: stringPtr(t.MarkdownText().Hex()),
			},
		},
	}
}
