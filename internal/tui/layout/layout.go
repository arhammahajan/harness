package layout

import (
	"reflect"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Focusable is implemented by components that can receive and release input focus.
type Focusable interface {
	Focus() tea.Cmd
	Blur() tea.Cmd
	IsFocused() bool
}

// Sizeable is implemented by components whose dimensions can be set and queried.
type Sizeable interface {
	SetSize(width, height int) tea.Cmd
	GetSize() (int, int)
}

// Bindings is implemented by components that advertise key bindings for help display.
type Bindings interface {
	BindingKeys() []key.Binding
}

// Drawable is implemented by components that render directly into an Ultraviolet
// cell screen buffer and optionally return their desired terminal cursor.
type Drawable interface {
	Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor
}

// KeyMapToSlice extracts all key.Binding fields from a struct key map.
func KeyMapToSlice(t any) (bindings []key.Binding) {
	if t == nil {
		return nil
	}
	typ := reflect.TypeOf(t)
	if typ.Kind() != reflect.Struct {
		return nil
	}
	val := reflect.ValueOf(t)
	for i := range typ.NumField() {
		v := val.Field(i)
		if !v.CanInterface() {
			continue
		}
		if b, ok := v.Interface().(key.Binding); ok {
			bindings = append(bindings, b)
		}
	}
	return bindings
}
