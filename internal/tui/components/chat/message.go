package chat

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/arhammahajan/harness/internal/diff"
	"github.com/arhammahajan/harness/internal/llm/agent"
	"github.com/arhammahajan/harness/internal/llm/models"
	"github.com/arhammahajan/harness/internal/llm/tools"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/tui/list"
	"github.com/arhammahajan/harness/internal/tui/markdown"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
)

const maxResultHeight = 10

// MessageItem represents a chat message or tool call item rendered inside the
// virtualized [list.List].
type MessageItem interface {
	list.Item
	ID() string
	ClearCache()
}

// assistantSection is a per-section render cache for AssistantMessageItem.
// Each section (thinking, content) carries its own width + content hash +
// metadata key so streaming body content never invalidates the cached thinking
// section.
type assistantSection struct {
	width   int
	srcHash uint64
	extra   uint64
	out     string
	valid   bool
	hits    int
	misses  int
}

func (s *assistantSection) hit(width int, srcHash, extra uint64) bool {
	return s.valid && s.width == width && s.srcHash == srcHash && s.extra == extra
}

func (s *assistantSection) store(width int, srcHash, extra uint64, out string) {
	s.width = width
	s.srcHash = srcHash
	s.extra = extra
	s.out = out
	s.valid = true
}

func (s *assistantSection) reset() {
	s.width = 0
	s.srcHash = 0
	s.extra = 0
	s.out = ""
	s.valid = false
}

func fnv64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func fnvFields(fields ...[]byte) uint64 {
	h := fnv.New64a()
	var lenBuf [8]byte
	for _, f := range fields {
		binary.LittleEndian.PutUint64(lenBuf[:], uint64(len(f)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write(f)
	}
	return h.Sum64()
}

func toMarkdown(content string, width int) string {
	r := styles.GetMarkdownRenderer(max(1, width))
	if r == nil {
		return content
	}
	mu := styles.LockMarkdownRenderer(r)
	mu.Lock()
	rendered, err := r.Render(content)
	mu.Unlock()
	if err != nil {
		return content
	}
	return strings.TrimSuffix(rendered, "\n")
}

// UserMessageItem renders a user chat message with Harness's left thick border
// and attachment badges, memoized by width.
type UserMessageItem struct {
	*list.Versioned

	msg       message.Message
	stateHash uint64

	cachedWidth  int
	cachedRender string
}

var _ MessageItem = (*UserMessageItem)(nil)

// NewUserMessageItem constructs a versioned, cached user message item.
func NewUserMessageItem(msg message.Message) *UserMessageItem {
	return &UserMessageItem{
		Versioned: list.NewVersioned(),
		msg:       msg,
		stateHash: userStateHash(msg),
	}
}

func (u *UserMessageItem) ID() string {
	return u.msg.ID
}

// SetMessage updates the underlying user message and bumps the version only
// when rendered content changes.
func (u *UserMessageItem) SetMessage(msg message.Message) {
	newHash := userStateHash(msg)
	if newHash == u.stateHash {
		return
	}
	u.msg = msg
	u.stateHash = newHash
	u.cachedWidth = 0
	u.cachedRender = ""
	u.Bump()
}

// ClearCache drops the cached rendered string and bumps the version.
func (u *UserMessageItem) ClearCache() {
	u.cachedWidth = 0
	u.cachedRender = ""
	u.Bump()
}

// Render renders the user message at width, returning the cached output on a
// width hit.
func (u *UserMessageItem) Render(width int) string {
	width = max(1, width)
	if u.cachedRender != "" && u.cachedWidth == width {
		return u.cachedRender
	}

	t := theme.CurrentTheme()
	innerWidth := max(1, width-1)

	var styledAttachments []string
	attachmentStyles := styles.BaseStyle().
		MarginLeft(1).
		Background(t.TextMuted()).
		Foreground(t.Text())
	for _, attachment := range u.msg.BinaryContent() {
		file := filepath.Base(attachment.Path)
		var filename string
		if len(file) > 10 {
			filename = fmt.Sprintf(" %s %s...", styles.DocumentIcon, file[0:7])
		} else {
			filename = fmt.Sprintf(" %s %s", styles.DocumentIcon, file)
		}
		styledAttachments = append(styledAttachments, attachmentStyles.Render(filename))
	}

	parts := []string{
		toMarkdown(u.msg.Content().String(), innerWidth),
	}
	if len(styledAttachments) > 0 {
		attachmentContent := styles.BaseStyle().
			Width(innerWidth).
			Render(lipgloss.JoinHorizontal(lipgloss.Top, styledAttachments...))
		parts = append(parts, attachmentContent)
	}

	style := styles.BaseStyle().
		Width(width).
		BorderLeft(true).
		Foreground(t.TextMuted()).
		BorderForeground(t.Secondary()).
		BorderStyle(lipgloss.ThickBorder())

	rendered := style.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			parts...,
		),
	)

	u.cachedWidth = width
	u.cachedRender = rendered
	return rendered
}

func userStateHash(msg message.Message) uint64 {
	fields := [][]byte{
		[]byte(msg.ID),
		[]byte(msg.Content().String()),
	}
	for _, b := range msg.BinaryContent() {
		fields = append(fields, []byte(b.Path), []byte(b.MIMEType))
	}
	return fnvFields(fields...)
}

// AssistantMessageItem renders an assistant message with per-section render
// caches and incremental StreamingMarkdown caches for both content and
// thinking sections.
type AssistantMessageItem struct {
	*list.Versioned

	msg       message.Message
	isSummary bool
	stateHash uint64

	// Incremental FNV-64a hash state for thinking text so long reasoning traces
	// avoid O(N) re-hashing on every streaming flush.
	thinkingHash       uint64
	thinkingHashLen    int
	thinkingHashSample string

	// Per-section caches: streaming content never invalidates thinkingSec.
	thinkingSec assistantSection
	contentSec  assistantSection

	// Stable-prefix streaming markdown renderers for O(delta) streaming.
	streamingContent  markdown.StreamingMarkdown
	streamingThinking markdown.StreamingMarkdown

	// Outer bordered-message cache keyed by (width, stateHash).
	cachedWidth  int
	cachedHash   uint64
	cachedRender string
}

var _ MessageItem = (*AssistantMessageItem)(nil)

// NewAssistantMessageItem constructs a versioned, cached assistant message item.
func NewAssistantMessageItem(msg message.Message, isSummary bool) *AssistantMessageItem {
	return &AssistantMessageItem{
		Versioned: list.NewVersioned(),
		msg:       msg,
		isSummary: isSummary,
		stateHash: assistantStateHash(msg, isSummary),
	}
}

func (a *AssistantMessageItem) ID() string {
	return a.msg.ID
}

// SetMessage updates the message in-place while preserving per-section caches
// and StreamingMarkdown stable prefixes. Calls Bump() only when state changed.
func (a *AssistantMessageItem) SetMessage(msg message.Message, isSummary bool) {
	newHash := assistantStateHash(msg, isSummary)
	if newHash == a.stateHash {
		return
	}
	a.msg = msg
	a.isSummary = isSummary
	a.stateHash = newHash
	a.cachedRender = ""
	a.cachedWidth = 0
	a.cachedHash = 0
	a.Bump()
}

// ClearCache drops all per-section caches, StreamingMarkdown caches, and the
// outer render cache, then bumps the version so the next draw re-renders with
// fresh styles.
func (a *AssistantMessageItem) ClearCache() {
	a.thinkingSec.reset()
	a.contentSec.reset()
	a.streamingContent.Reset()
	a.streamingThinking.Reset()
	a.thinkingHash = 0
	a.thinkingHashLen = 0
	a.thinkingHashSample = ""
	a.cachedRender = ""
	a.cachedWidth = 0
	a.cachedHash = 0
	a.Bump()
}

// Render renders the assistant message at width using per-section caches and
// incremental StreamingMarkdown rendering.
func (a *AssistantMessageItem) Render(width int) string {
	width = max(1, width)
	if a.cachedRender != "" && a.cachedWidth == width && a.cachedHash == a.stateHash {
		return a.cachedRender
	}

	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()
	innerWidth := max(1, width-1)

	content := a.msg.Content().String()
	thinkingContent := a.msg.ReasoningContent().Thinking
	finished := a.msg.IsFinished()
	finishData := a.msg.FinishPart()

	var parts []string
	if thinkingContent != "" {
		parts = append(parts, a.cachedThinking(innerWidth))
	}

	hasBody := content != "" || (finished && finishData != nil && finishData.Reason == message.FinishReasonEndTurn)
	if hasBody {
		parts = append(parts, a.cachedContent(innerWidth))
	}

	var info []string
	if finished && finishData != nil {
		modelName := models.SupportedModels[a.msg.Model].Name
		if modelName == "" {
			modelName = string(a.msg.Model)
		}
		switch finishData.Reason {
		case message.FinishReasonEndTurn:
			took := formatTimestampDiff(a.msg.CreatedAt, finishData.Time)
			info = append(info, baseStyle.
				Width(innerWidth).
				Foreground(t.TextMuted()).
				Render(fmt.Sprintf(" %s (%s)", modelName, took)),
			)
		case message.FinishReasonCanceled:
			info = append(info, baseStyle.
				Width(innerWidth).
				Foreground(t.TextMuted()).
				Render(fmt.Sprintf(" %s (%s)", modelName, "canceled")),
			)
		case message.FinishReasonError:
			info = append(info, baseStyle.
				Width(innerWidth).
				Foreground(t.TextMuted()).
				Render(fmt.Sprintf(" %s (%s)", modelName, "error")),
			)
		case message.FinishReasonPermissionDenied:
			info = append(info, baseStyle.
				Width(innerWidth).
				Foreground(t.TextMuted()).
				Render(fmt.Sprintf(" %s (%s)", modelName, "permission denied")),
			)
		}
	}
	if hasBody && a.isSummary {
		info = append(info, baseStyle.
			Width(innerWidth).
			Foreground(t.TextMuted()).
			Render(" (summary)"),
		)
	}

	parts = append(parts, info...)

	style := baseStyle.
		Width(width).
		BorderLeft(true).
		Foreground(t.TextMuted()).
		BorderForeground(t.Primary()).
		BorderStyle(lipgloss.ThickBorder())

	rendered := style.Render(
		lipgloss.JoinVertical(
			lipgloss.Left,
			parts...,
		),
	)

	a.cachedWidth = width
	a.cachedHash = a.stateHash
	a.cachedRender = rendered
	return rendered
}

func (a *AssistantMessageItem) thinkingHashIncremental(thinking string) uint64 {
	sampleLen := min(len(thinking), 64)
	if a.thinkingHashLen > 0 && len(thinking) >= a.thinkingHashLen &&
		thinking[:sampleLen] == a.thinkingHashSample {
		h := a.thinkingHash
		for i := a.thinkingHashLen; i < len(thinking); i++ {
			h ^= uint64(thinking[i])
			h *= 1099511628211
		}
		a.thinkingHash = h
		a.thinkingHashLen = len(thinking)
		return h
	}
	h := fnv64(thinking)
	a.thinkingHash = h
	a.thinkingHashLen = len(thinking)
	a.thinkingHashSample = thinking[:sampleLen]
	return h
}

func (a *AssistantMessageItem) cachedThinking(width int) string {
	thinking := a.msg.ReasoningContent().Thinking
	srcHash := a.thinkingHashIncremental(thinking)
	const extra uint64 = 0
	if a.thinkingSec.hit(width, srcHash, extra) {
		a.thinkingSec.hits++
		return a.thinkingSec.out
	}
	a.thinkingSec.misses++
	r := styles.GetMarkdownRenderer(max(1, width))
	out := a.streamingThinking.Render(thinking, max(1, width), r)
	a.thinkingSec.store(width, srcHash, extra, out)
	return out
}

func (a *AssistantMessageItem) cachedContent(width int) string {
	content := a.msg.Content().String()
	fallback := uint64(0)
	if content == "" {
		content = "*Finished without output*"
		fallback = 1
	}
	srcHash := fnv64(content)
	if a.contentSec.hit(width, srcHash, fallback) {
		a.contentSec.hits++
		return a.contentSec.out
	}
	a.contentSec.misses++
	r := styles.GetMarkdownRenderer(max(1, width))
	out := a.streamingContent.Render(content, max(1, width), r)
	a.contentSec.store(width, srcHash, fallback, out)
	return out
}

func assistantStateHash(msg message.Message, isSummary bool) uint64 {
	var flags [3]byte
	if msg.IsThinking() {
		flags[0] = 1
	}
	if msg.IsFinished() {
		flags[1] = 1
	}
	if isSummary {
		flags[2] = 1
	}
	var finishReason string
	var finishTime [8]byte
	if fp := msg.FinishPart(); fp != nil {
		finishReason = string(fp.Reason)
		binary.LittleEndian.PutUint64(finishTime[:], uint64(fp.Time))
	}
	var createdAt [8]byte
	binary.LittleEndian.PutUint64(createdAt[:], uint64(msg.CreatedAt))

	return fnvFields(
		[]byte(msg.ID),
		[]byte(msg.Model),
		[]byte(msg.Content().String()),
		[]byte(msg.ReasoningContent().Thinking),
		flags[:],
		[]byte(finishReason),
		finishTime[:],
		createdAt[:],
	)
}

func shouldRenderAssistantMessage(msg message.Message) bool {
	if msg.Content().String() != "" {
		return true
	}
	if msg.ReasoningContent().Thinking != "" {
		return true
	}
	if msg.IsFinished() {
		if fp := msg.FinishPart(); fp != nil && fp.Reason != message.FinishReasonToolUse {
			return true
		}
	}
	return false
}

// ToolMessageItem renders a tool call and its result (plus any nested Task
// sub-agent tool calls), cached by width and bumped only when tool state
// changes.
type ToolMessageItem struct {
	*list.Versioned

	toolCall message.ToolCall
	result   *message.ToolResult
	nested   []message.ToolCall

	stateHash    uint64
	cachedWidth  int
	cachedRender string
}

var _ MessageItem = (*ToolMessageItem)(nil)

// NewToolMessageItem constructs a versioned, cached tool call item.
func NewToolMessageItem(tc message.ToolCall, result *message.ToolResult, nested []message.ToolCall) *ToolMessageItem {
	var resCopy *message.ToolResult
	if result != nil {
		r := *result
		resCopy = &r
	}
	var nestedCopy []message.ToolCall
	if len(nested) > 0 {
		nestedCopy = append([]message.ToolCall(nil), nested...)
	}
	return &ToolMessageItem{
		Versioned: list.NewVersioned(),
		toolCall:  tc,
		result:    resCopy,
		nested:    nestedCopy,
		stateHash: toolStateHash(tc, resCopy, nestedCopy),
	}
}

func (t *ToolMessageItem) ID() string {
	return t.toolCall.ID
}

// SetToolCall updates the tool call, result, and nested tool calls. It clears
// the render cache and calls Bump() only when the state changed.
func (t *ToolMessageItem) SetToolCall(tc message.ToolCall, result *message.ToolResult, nested []message.ToolCall) {
	newHash := toolStateHash(tc, result, nested)
	if newHash == t.stateHash {
		return
	}
	t.toolCall = tc
	if result != nil {
		r := *result
		t.result = &r
	} else {
		t.result = nil
	}
	if len(nested) > 0 {
		t.nested = append([]message.ToolCall(nil), nested...)
	} else {
		t.nested = nil
	}
	t.stateHash = newHash
	t.cachedWidth = 0
	t.cachedRender = ""
	t.Bump()
}

// ClearCache drops the cached rendered string and bumps the version.
func (t *ToolMessageItem) ClearCache() {
	t.cachedWidth = 0
	t.cachedRender = ""
	t.Bump()
}

// Render renders the tool call at width, returning the cached output on hit.
func (t *ToolMessageItem) Render(width int) string {
	width = max(1, width)
	if t.cachedRender != "" && t.cachedWidth == width {
		return t.cachedRender
	}
	rendered := renderToolCallBox(t.toolCall, t.result, t.nested, false, width)
	t.cachedWidth = width
	t.cachedRender = rendered
	return rendered
}

func toolStateHash(tc message.ToolCall, result *message.ToolResult, nested []message.ToolCall) uint64 {
	var tcFinished byte
	if tc.Finished {
		tcFinished = 1
	}
	fields := [][]byte{
		[]byte(tc.ID),
		[]byte(tc.Name),
		[]byte(tc.Input),
		[]byte(tc.Type),
		{tcFinished},
	}
	if result != nil {
		var isErr byte
		if result.IsError {
			isErr = 1
		}
		fields = append(fields,
			[]byte{1, isErr},
			[]byte(result.ToolCallID),
			[]byte(result.Name),
			[]byte(result.Content),
			[]byte(result.Metadata),
		)
	} else {
		fields = append(fields, []byte{0})
	}
	for _, n := range nested {
		var nf byte
		if n.Finished {
			nf = 1
		}
		fields = append(fields, []byte(n.ID), []byte(n.Name), []byte(n.Input), []byte{nf})
	}
	return fnvFields(fields...)
}

func toolName(name string) string {
	switch name {
	case agent.AgentToolName:
		return "Task"
	case tools.BashToolName:
		return "Bash"
	case tools.EditToolName:
		return "Edit"
	case tools.FetchToolName:
		return "Fetch"
	case tools.GlobToolName:
		return "Glob"
	case tools.GrepToolName:
		return "Grep"
	case tools.LSToolName:
		return "List"
	case tools.SourcegraphToolName:
		return "Sourcegraph"
	case tools.ViewToolName:
		return "View"
	case tools.WriteToolName:
		return "Write"
	case tools.PatchToolName:
		return "Patch"
	}
	return name
}

func getToolAction(name string) string {
	switch name {
	case agent.AgentToolName:
		return "Preparing prompt..."
	case tools.BashToolName:
		return "Building command..."
	case tools.EditToolName:
		return "Preparing edit..."
	case tools.FetchToolName:
		return "Writing fetch..."
	case tools.GlobToolName:
		return "Finding files..."
	case tools.GrepToolName:
		return "Searching content..."
	case tools.LSToolName:
		return "Listing directory..."
	case tools.SourcegraphToolName:
		return "Searching code..."
	case tools.ViewToolName:
		return "Reading file..."
	case tools.WriteToolName:
		return "Preparing write..."
	case tools.PatchToolName:
		return "Preparing patch..."
	}
	return "Working..."
}

// renderParams formats params as: params[0] (params[1]=params[2], ...).
func renderParams(paramsWidth int, params ...string) string {
	if len(params) == 0 || paramsWidth <= 0 {
		return ""
	}
	mainParam := params[0]
	if ansi.StringWidth(mainParam) > paramsWidth {
		if paramsWidth <= 3 {
			mainParam = ansi.Truncate(mainParam, paramsWidth, "")
		} else {
			mainParam = ansi.Truncate(mainParam, paramsWidth, "...")
		}
	}

	if len(params) == 1 {
		return mainParam
	}
	otherParams := params[1:]
	if len(otherParams)%2 != 0 {
		otherParams = append(otherParams, "")
	}
	parts := make([]string, 0, len(otherParams)/2)
	for i := 0; i < len(otherParams); i += 2 {
		key := otherParams[i]
		value := otherParams[i+1]
		if value == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}

	partsRendered := strings.Join(parts, ", ")
	remainingWidth := paramsWidth - lipgloss.Width(partsRendered) - 5
	if remainingWidth < 30 {
		return mainParam
	}

	if len(parts) > 0 {
		mainParam = fmt.Sprintf("%s (%s)", mainParam, partsRendered)
	}

	return ansi.Truncate(mainParam, paramsWidth, "...")
}

func removeWorkingDirPrefix(path string) string {
	wd := workingDirectory()
	if wd != "" && strings.HasPrefix(path, wd) {
		path = strings.TrimPrefix(path, wd)
	}
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "../")
	return path
}

func renderToolParams(paramWidth int, toolCall message.ToolCall) string {
	switch toolCall.Name {
	case agent.AgentToolName:
		var params agent.AgentParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		prompt := strings.ReplaceAll(params.Prompt, "\n", " ")
		return renderParams(paramWidth, prompt)
	case tools.BashToolName:
		var params tools.BashParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		command := strings.ReplaceAll(params.Command, "\n", " ")
		return renderParams(paramWidth, command)
	case tools.EditToolName:
		var params tools.EditParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		filePath := removeWorkingDirPrefix(params.FilePath)
		return renderParams(paramWidth, filePath)
	case tools.FetchToolName:
		var params tools.FetchParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		toolParams := []string{params.URL}
		if params.Format != "" {
			toolParams = append(toolParams, "format", params.Format)
		}
		if params.Timeout != 0 {
			toolParams = append(toolParams, "timeout", (time.Duration(params.Timeout) * time.Second).String())
		}
		return renderParams(paramWidth, toolParams...)
	case tools.GlobToolName:
		var params tools.GlobParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		toolParams := []string{params.Pattern}
		if params.Path != "" {
			toolParams = append(toolParams, "path", params.Path)
		}
		return renderParams(paramWidth, toolParams...)
	case tools.GrepToolName:
		var params tools.GrepParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		toolParams := []string{params.Pattern}
		if params.Path != "" {
			toolParams = append(toolParams, "path", params.Path)
		}
		if params.Include != "" {
			toolParams = append(toolParams, "include", params.Include)
		}
		if params.LiteralText {
			toolParams = append(toolParams, "literal", "true")
		}
		return renderParams(paramWidth, toolParams...)
	case tools.LSToolName:
		var params tools.LSParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		path := params.Path
		if path == "" {
			path = "."
		}
		return renderParams(paramWidth, path)
	case tools.SourcegraphToolName:
		var params tools.SourcegraphParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		return renderParams(paramWidth, params.Query)
	case tools.ViewToolName:
		var params tools.ViewParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		filePath := removeWorkingDirPrefix(params.FilePath)
		toolParams := []string{filePath}
		if params.Limit != 0 {
			toolParams = append(toolParams, "limit", fmt.Sprintf("%d", params.Limit))
		}
		if params.Offset != 0 {
			toolParams = append(toolParams, "offset", fmt.Sprintf("%d", params.Offset))
		}
		return renderParams(paramWidth, toolParams...)
	case tools.WriteToolName:
		var params tools.WriteParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		filePath := removeWorkingDirPrefix(params.FilePath)
		return renderParams(paramWidth, filePath)
	default:
		input := strings.ReplaceAll(toolCall.Input, "\n", " ")
		return renderParams(paramWidth, input)
	}
}

func truncateHeight(content string, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		return strings.Join(lines[:height], "\n")
	}
	return content
}

func renderToolResponse(toolCall message.ToolCall, response message.ToolResult, width int) string {
	width = max(1, width)
	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	if response.IsError {
		errContent := fmt.Sprintf("Error: %s", strings.ReplaceAll(response.Content, "\n", " "))
		errContent = ansi.Truncate(errContent, max(1, width-1), "...")
		return baseStyle.
			Width(width).
			Foreground(t.Error()).
			Render(errContent)
	}

	resultContent := truncateHeight(response.Content, maxResultHeight)
	switch toolCall.Name {
	case agent.AgentToolName:
		return toMarkdown(resultContent, width)
	case tools.BashToolName:
		resultContent = fmt.Sprintf("```bash\n%s\n```", resultContent)
		return toMarkdown(resultContent, width)
	case tools.EditToolName:
		metadata := tools.EditResponseMetadata{}
		_ = json.Unmarshal([]byte(response.Metadata), &metadata)
		truncDiff := truncateHeight(metadata.Diff, maxResultHeight)
		formattedDiff, _ := diff.FormatDiff(truncDiff, diff.WithTotalWidth(width))
		return formattedDiff
	case tools.FetchToolName:
		var params tools.FetchParams
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		mdFormat := "markdown"
		switch params.Format {
		case "text":
			mdFormat = "text"
		case "html":
			mdFormat = "html"
		}
		resultContent = fmt.Sprintf("```%s\n%s\n```", mdFormat, resultContent)
		return toMarkdown(resultContent, width)
	case tools.GlobToolName, tools.GrepToolName, tools.LSToolName, tools.SourcegraphToolName:
		return baseStyle.Width(width).Foreground(t.TextMuted()).Render(resultContent)
	case tools.ViewToolName:
		metadata := tools.ViewResponseMetadata{}
		_ = json.Unmarshal([]byte(response.Metadata), &metadata)
		ext := filepath.Ext(metadata.FilePath)
		if ext != "" {
			ext = strings.ToLower(ext[1:])
		}
		resultContent = fmt.Sprintf("```%s\n%s\n```", ext, truncateHeight(metadata.Content, maxResultHeight))
		return toMarkdown(resultContent, width)
	case tools.WriteToolName:
		params := tools.WriteParams{}
		_ = json.Unmarshal([]byte(toolCall.Input), &params)
		ext := filepath.Ext(params.FilePath)
		if ext != "" {
			ext = strings.ToLower(ext[1:])
		}
		resultContent = fmt.Sprintf("```%s\n%s\n```", ext, truncateHeight(params.Content, maxResultHeight))
		return toMarkdown(resultContent, width)
	default:
		resultContent = fmt.Sprintf("```text\n%s\n```", resultContent)
		return toMarkdown(resultContent, width)
	}
}

func renderToolCallBox(
	toolCall message.ToolCall,
	response *message.ToolResult,
	nestedCalls []message.ToolCall,
	nested bool,
	width int,
) string {
	if nested {
		width = max(1, width-3)
	}
	innerWidth := max(1, width-2)

	t := theme.CurrentTheme()
	baseStyle := styles.BaseStyle()

	style := baseStyle.
		Width(width).
		BorderLeft(true).
		BorderStyle(lipgloss.ThickBorder()).
		PaddingLeft(1).
		BorderForeground(t.TextMuted())

	toolNameText := baseStyle.Foreground(t.TextMuted()).
		Render(fmt.Sprintf("%s: ", toolName(toolCall.Name)))
	toolNameWidth := lipgloss.Width(toolNameText)
	paramAvail := max(1, innerWidth-toolNameWidth)

	if !toolCall.Finished {
		toolAction := getToolAction(toolCall.Name)
		progressText := baseStyle.
			Width(paramAvail).
			Foreground(t.TextMuted()).
			Render(toolAction)

		line := lipgloss.JoinHorizontal(lipgloss.Top, toolNameText, progressText)
		if nested {
			prefix := baseStyle.Foreground(t.TextMuted()).Render(" └ ")
			return lipgloss.JoinHorizontal(lipgloss.Top, prefix, line)
		}
		return style.Render(line)
	}

	params := renderToolParams(paramAvail, toolCall)

	var parts []string
	if !nested {
		formattedParams := baseStyle.
			Width(paramAvail).
			Foreground(t.TextMuted()).
			Render(params)
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, toolNameText, formattedParams))
	} else {
		prefix := baseStyle.
			Foreground(t.TextMuted()).
			Render(" └ ")
		nestedParamAvail := max(1, innerWidth-toolNameWidth-lipgloss.Width(prefix))
		formattedParams := baseStyle.
			Width(nestedParamAvail).
			Foreground(t.TextMuted()).
			Render(params)
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, prefix, toolNameText, formattedParams))
	}

	if toolCall.Name == agent.AgentToolName && !nested {
		for _, call := range nestedCalls {
			parts = append(parts, renderToolCallBox(call, nil, nil, true, width))
		}
	}

	if !nested {
		var responseContent string
		if response != nil {
			responseContent = renderToolResponse(toolCall, *response, innerWidth)
			responseContent = strings.TrimSuffix(responseContent, "\n")
		} else {
			responseContent = baseStyle.
				Italic(true).
				Width(innerWidth).
				Foreground(t.TextMuted()).
				Render("Waiting for response...")
		}
		if responseContent != "" {
			parts = append(parts, responseContent)
		}
		return style.Render(
			lipgloss.JoinVertical(
				lipgloss.Left,
				parts...,
			),
		)
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		parts...,
	)
}

// formatTimestampDiff formats the time difference between two Unix timestamps.
func formatTimestampDiff(start, end int64) string {
	diffSeconds := float64(end-start) / 1000.0
	if diffSeconds < 1 {
		return fmt.Sprintf("%dms", int(diffSeconds*1000))
	}
	if diffSeconds < 60 {
		return fmt.Sprintf("%.1fs", diffSeconds)
	}
	return fmt.Sprintf("%.1fm", diffSeconds/60)
}
