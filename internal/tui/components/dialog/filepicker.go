package dialog

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/arhammahajan/harness/internal/app"
	"github.com/arhammahajan/harness/internal/config"
	"github.com/arhammahajan/harness/internal/logging"
	"github.com/arhammahajan/harness/internal/message"
	"github.com/arhammahajan/harness/internal/tui/image"
	"github.com/arhammahajan/harness/internal/tui/layout"
	"github.com/arhammahajan/harness/internal/tui/styles"
	"github.com/arhammahajan/harness/internal/tui/theme"
	"github.com/arhammahajan/harness/internal/tui/util"
)

const (
	maxAttachmentSize = int64(5 * 1024 * 1024) // 5MB
	downArrow         = "down"
	upArrow           = "up"
)

type filePreviewMsg struct {
	content string
}

type FilePrickerKeyMap struct {
	Enter          key.Binding
	Down           key.Binding
	Up             key.Binding
	Forward        key.Binding
	Backward       key.Binding
	OpenFilePicker key.Binding
	Esc            key.Binding
	InsertCWD      key.Binding
}

var filePickerKeyMap = FilePrickerKeyMap{
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select file/enter directory"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", downArrow),
		key.WithHelp("↓/j", "down"),
	),
	Up: key.NewBinding(
		key.WithKeys("k", upArrow),
		key.WithHelp("↑/k", "up"),
	),
	Forward: key.NewBinding(
		key.WithKeys("l"),
		key.WithHelp("l", "enter directory"),
	),
	Backward: key.NewBinding(
		key.WithKeys("h", "backspace"),
		key.WithHelp("h/backspace", "go back"),
	),
	OpenFilePicker: key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "open file picker"),
	),
	Esc: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "close/exit"),
	),
	InsertCWD: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "manual path input"),
	),
}

type filepickerCmp struct {
	basePath       string
	width          int
	height         int
	cursor         int
	err            error
	cursorChain    stack
	viewport       viewport.Model
	dirs           []os.DirEntry
	cwdDetails     *DirNode
	selectedFile   string
	cwd            textinput.Model
	ShowFilePicker bool
	app            *app.App
}

type DirNode struct {
	parent    *DirNode
	child     *DirNode
	directory string
}
type stack []int

func (s stack) Push(v int) stack {
	return append(s, v)
}

func (s stack) Pop() (stack, int) {
	l := len(s)
	return s[:l-1], s[l-1]
}

type AttachmentAddedMsg struct {
	Attachment message.Attachment
}

func (f *filepickerCmp) Init() tea.Cmd {
	return nil
}

func (f *filepickerCmp) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case filePreviewMsg:
		f.viewport.SetContent(msg.content)
		return f, nil
	case tea.WindowSizeMsg:
		f.width = 60
		f.height = 20
		f.viewport.SetWidth(80)
		f.viewport.SetHeight(22)
		f.cursor = 0
		if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case tea.KeyPressMsg:
		if f.cwd.Focused() {
			var cmd tea.Cmd
			f.cwd, cmd = f.cwd.Update(msg)
			cmds = append(cmds, cmd)
		}
		switch {
		case key.Matches(msg, filePickerKeyMap.InsertCWD):
			if !f.cwd.Focused() {
				cmds = append(cmds, f.cwd.Focus())
				return f, tea.Batch(cmds...)
			}
		case key.Matches(msg, filePickerKeyMap.Esc):
			if f.cwd.Focused() {
				f.cwd.Blur()
			} else {
				f.cursorChain = make(stack, 0)
				f.cursor = 0
			}
		case key.Matches(msg, filePickerKeyMap.Down):
			if !f.cwd.Focused() || msg.String() == downArrow {
				if f.cursor < len(f.dirs)-1 {
					f.cursor++
					if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		case key.Matches(msg, filePickerKeyMap.Up):
			if !f.cwd.Focused() || msg.String() == upArrow {
				if f.cursor > 0 {
					f.cursor--
					if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		case key.Matches(msg, filePickerKeyMap.Enter):
			var path string
			var isPathDir bool
			if f.cwd.Focused() {
				path = f.cwd.Value()
				fileInfo, err := os.Stat(path)
				if err != nil {
					logging.ErrorPersist("Invalid path")
					return f, tea.Batch(cmds...)
				}
				isPathDir = fileInfo.IsDir()
			} else {
				if len(f.dirs) == 0 || f.cursor < 0 || f.cursor >= len(f.dirs) {
					return f, tea.Batch(cmds...)
				}
				path = filepath.Join(f.cwdDetails.directory, f.dirs[f.cursor].Name())
				isPathDir = f.dirs[f.cursor].IsDir()
			}
			if isPathDir {
				newWorkingDir := DirNode{parent: f.cwdDetails, directory: path}
				f.cwdDetails.child = &newWorkingDir
				f.cwdDetails = f.cwdDetails.child
				f.cursorChain = f.cursorChain.Push(f.cursor)
				f.dirs = readDir(f.cwdDetails.directory, false)
				f.cursor = 0
				f.cwd.SetValue(f.cwdDetails.directory)
				if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			} else {
				f.selectedFile = path
				return f.addAttachmentToMessage()
			}
		case key.Matches(msg, filePickerKeyMap.Forward):
			if !f.cwd.Focused() {
				if len(f.dirs) > 0 && f.cursor >= 0 && f.cursor < len(f.dirs) && f.dirs[f.cursor].IsDir() {
					path := filepath.Join(f.cwdDetails.directory, f.dirs[f.cursor].Name())
					newWorkingDir := DirNode{parent: f.cwdDetails, directory: path}
					f.cwdDetails.child = &newWorkingDir
					f.cwdDetails = f.cwdDetails.child
					f.cursorChain = f.cursorChain.Push(f.cursor)
					f.dirs = readDir(f.cwdDetails.directory, false)
					f.cursor = 0
					f.cwd.SetValue(f.cwdDetails.directory)
					if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		case key.Matches(msg, filePickerKeyMap.Backward):
			if !f.cwd.Focused() {
				if len(f.cursorChain) != 0 && f.cwdDetails.parent != nil {
					f.cursorChain, f.cursor = f.cursorChain.Pop()
					f.cwdDetails = f.cwdDetails.parent
					f.cwdDetails.child = nil
					f.dirs = readDir(f.cwdDetails.directory, false)
					f.cwd.SetValue(f.cwdDetails.directory)
					if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		case key.Matches(msg, filePickerKeyMap.OpenFilePicker):
			f.dirs = readDir(f.cwdDetails.directory, false)
			f.cursor = 0
			if cmd := f.getCurrentFileBelowCursor(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return f, tea.Batch(cmds...)
}

func (f *filepickerCmp) addAttachmentToMessage() (tea.Model, tea.Cmd) {
	modeInfo := GetSelectedModel(config.Get())
	if !modeInfo.SupportsAttachments {
		logging.ErrorPersist(fmt.Sprintf("Model %s doesn't support attachments", modeInfo.Name))
		return f, nil
	}

	selectedFilePath := f.selectedFile
	if !isExtSupported(selectedFilePath) {
		logging.ErrorPersist("Unsupported file")
		return f, nil
	}

	isFileLarge, err := image.ValidateFileSize(selectedFilePath, maxAttachmentSize)
	if err != nil {
		logging.ErrorPersist("unable to read the image")
		return f, nil
	}
	if isFileLarge {
		logging.ErrorPersist("file too large, max 5MB")
		return f, nil
	}

	content, err := os.ReadFile(selectedFilePath)
	if err != nil {
		logging.ErrorPersist("Unable read selected file")
		return f, nil
	}

	mimeBufferSize := min(512, len(content))
	mimeType := http.DetectContentType(content[:mimeBufferSize])
	fileName := filepath.Base(selectedFilePath)
	attachment := message.Attachment{FilePath: selectedFilePath, FileName: fileName, MimeType: mimeType, Content: content}
	f.selectedFile = ""
	return f, util.CmdHandler(AttachmentAddedMsg{attachment})
}

func (f *filepickerCmp) render() (string, *tea.Cursor) {
	t := theme.CurrentTheme()
	const maxVisibleDirs = 20
	const maxWidth = 80

	adjustedWidth := maxWidth
	for _, file := range f.dirs {
		if len(file.Name()) > adjustedWidth-4 { // Account for padding
			adjustedWidth = len(file.Name()) + 4
		}
	}
	adjustedWidth = max(30, min(adjustedWidth, f.width-15)) + 1

	files := make([]string, 0, maxVisibleDirs)
	startIdx := 0

	if len(f.dirs) > maxVisibleDirs {
		halfVisible := maxVisibleDirs / 2
		if f.cursor >= halfVisible && f.cursor < len(f.dirs)-halfVisible {
			startIdx = f.cursor - halfVisible
		} else if f.cursor >= len(f.dirs)-halfVisible {
			startIdx = len(f.dirs) - maxVisibleDirs
		}
	}

	endIdx := min(startIdx+maxVisibleDirs, len(f.dirs))

	for i := startIdx; i < endIdx; i++ {
		file := f.dirs[i]
		itemStyle := styles.BaseStyle().Width(adjustedWidth)

		if i == f.cursor {
			itemStyle = itemStyle.
				Background(t.Primary()).
				Foreground(t.Background()).
				Bold(true)
		}
		filename := file.Name()

		if len(filename) > adjustedWidth-4 {
			filename = filename[:adjustedWidth-7] + "..."
		}
		if file.IsDir() {
			filename = filename + "/"
		}

		files = append(files, itemStyle.Padding(0, 1).Render(filename))
	}

	// Pad to always show exactly 20 lines
	for len(files) < maxVisibleDirs {
		files = append(files, styles.BaseStyle().Width(adjustedWidth).Render(""))
	}

	currentPath := styles.BaseStyle().
		Height(1).
		Width(adjustedWidth).
		Render(f.cwd.View())

	viewportWidth := f.viewport.Width()
	if viewportWidth <= 0 {
		viewportWidth = 40
	}
	viewportstyle := lipgloss.NewStyle().
		Width(viewportWidth).
		Background(t.Background()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.TextMuted()).
		BorderBackground(t.Background()).
		Padding(2).
		Render(f.viewport.View())

	var insertExitText string
	if f.IsCWDFocused() {
		insertExitText = "Press esc to exit typing path"
	} else {
		insertExitText = "Press i to start typing path"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		currentPath,
		styles.BaseStyle().Width(adjustedWidth).Render(""),
		styles.BaseStyle().Width(adjustedWidth).Render(lipgloss.JoinVertical(lipgloss.Left, files...)),
		styles.BaseStyle().Width(adjustedWidth).Render(""),
		styles.BaseStyle().Foreground(t.TextMuted()).Width(adjustedWidth).Render(insertExitText),
	)

	contentStyle := styles.BaseStyle().Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderBackground(t.Background()).
		BorderForeground(t.TextMuted()).
		Width(lipgloss.Width(content) + 6)

	leftPane := contentStyle.Render(content)
	combined := lipgloss.JoinHorizontal(lipgloss.Center, leftPane, viewportstyle)

	var cur *tea.Cursor
	if f.cwd.Focused() {
		if c := f.cwd.Cursor(); c != nil {
			leftH := lipgloss.Height(leftPane)
			totalH := lipgloss.Height(combined)
			yPad := max(0, (totalH-leftH)/2)
			// Left pane: border (1) + padding (1 top, 2 left).
			c.X += 1 + 2
			c.Y += 1 + 1 + yPad
			cur = c
		}
	}

	return combined, cur
}

func (f *filepickerCmp) View() tea.View {
	content, cur := f.render()
	v := tea.NewView(content)
	v.Cursor = cur
	return v
}

func (f *filepickerCmp) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	content, cur := f.render()
	layout.DrawCenterCursor(scr, area, content, cur)
	return cur
}

type FilepickerCmp interface {
	tea.Model
	layout.Drawable
	ToggleFilepicker(showFilepicker bool)
	IsCWDFocused() bool
}

func (f *filepickerCmp) ToggleFilepicker(showFilepicker bool) {
	f.ShowFilePicker = showFilepicker
}

func (f *filepickerCmp) IsCWDFocused() bool {
	return f.cwd.Focused()
}

func NewFilepickerCmp(app *app.App) FilepickerCmp {
	homepath, err := os.UserHomeDir()
	if err != nil {
		logging.Error("error loading user files")
		return nil
	}
	baseDir := DirNode{parent: nil, directory: homepath}
	dirs := readDir(homepath, false)
	vp := viewport.New()
	currentDirectory := textinput.New()
	currentDirectory.CharLimit = 200
	currentDirectory.SetWidth(44)
	currentDirectory.SetVirtualCursor(false)
	cwdStyles := currentDirectory.Styles()
	cwdStyles.Cursor.Blink = true
	currentDirectory.SetStyles(cwdStyles)
	currentDirectory.SetValue(baseDir.directory)
	return &filepickerCmp{
		cwdDetails:  &baseDir,
		dirs:        dirs,
		cursorChain: make(stack, 0),
		viewport:    vp,
		cwd:         currentDirectory,
		app:         app,
	}
}

func (f *filepickerCmp) getCurrentFileBelowCursor() tea.Cmd {
	if len(f.dirs) == 0 || f.cursor < 0 || f.cursor >= len(f.dirs) {
		logging.Error(fmt.Sprintf("Invalid cursor position. Dirs length: %d, Cursor: %d", len(f.dirs), f.cursor))
		f.viewport.SetContent("Preview unavailable")
		return nil
	}

	dir := f.dirs[f.cursor]
	filename := dir.Name()
	if !dir.IsDir() && isExtSupported(filename) {
		fullPath := filepath.Join(f.cwdDetails.directory, dir.Name())
		previewWidth := max(1, f.viewport.Width()-4)

		return func() tea.Msg {
			imageString, err := image.ImagePreview(previewWidth, fullPath)
			if err != nil {
				logging.Error(err.Error())
				return filePreviewMsg{content: "Preview unavailable"}
			}
			return filePreviewMsg{content: imageString}
		}
	}

	f.viewport.SetContent("Preview unavailable")
	return nil
}

func readDir(path string, showHidden bool) []os.DirEntry {
	logging.Info(fmt.Sprintf("Reading directory: %s", path))

	entriesChan := make(chan []os.DirEntry, 1)
	errChan := make(chan error, 1)

	go func() {
		dirEntries, err := os.ReadDir(path)
		if err != nil {
			logging.ErrorPersist(err.Error())
			errChan <- err
			return
		}
		entriesChan <- dirEntries
	}()

	select {
	case dirEntries := <-entriesChan:
		sort.Slice(dirEntries, func(i, j int) bool {
			if dirEntries[i].IsDir() == dirEntries[j].IsDir() {
				return dirEntries[i].Name() < dirEntries[j].Name()
			}
			return dirEntries[i].IsDir()
		})

		if showHidden {
			return dirEntries
		}

		var sanitizedDirEntries []os.DirEntry
		for _, dirEntry := range dirEntries {
			isHidden, _ := IsHidden(dirEntry.Name())
			if !isHidden {
				if dirEntry.IsDir() || isExtSupported(dirEntry.Name()) {
					sanitizedDirEntries = append(sanitizedDirEntries, dirEntry)
				}
			}
		}

		return sanitizedDirEntries

	case err := <-errChan:
		logging.ErrorPersist(fmt.Sprintf("Error reading directory %s", path), err)
		return []os.DirEntry{}

	case <-time.After(5 * time.Second):
		logging.ErrorPersist(fmt.Sprintf("Timeout reading directory %s", path), nil)
		return []os.DirEntry{}
	}
}

func IsHidden(file string) (bool, error) {
	return strings.HasPrefix(file, "."), nil
}

func isExtSupported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return (ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".png")
}
