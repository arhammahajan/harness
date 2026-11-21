package theme

import (
	"image/color"
	"sync"
	"sync/atomic"
	"testing"
)

func TestThemeRegistration(t *testing.T) {
	// Get list of available themes
	availableThemes := AvailableThemes()
	
	// Check if "catppuccin" theme is registered
	catppuccinFound := false
	for _, themeName := range availableThemes {
		if themeName == "catppuccin" {
			catppuccinFound = true
			break
		}
	}
	
	if !catppuccinFound {
		t.Errorf("Catppuccin theme is not registered")
	}
	
	// Check if "gruvbox" theme is registered
	gruvboxFound := false
	for _, themeName := range availableThemes {
		if themeName == "gruvbox" {
			gruvboxFound = true
			break
		}
	}
	
	if !gruvboxFound {
		t.Errorf("Gruvbox theme is not registered")
	}
	
	// Check if "monokai" theme is registered
	monokaiFound := false
	for _, themeName := range availableThemes {
		if themeName == "monokai" {
			monokaiFound = true
			break
		}
	}
	
	if !monokaiFound {
		t.Errorf("Monokai theme is not registered")
	}
	
	// Try to get the themes and make sure they're not nil
	catppuccin := GetTheme("catppuccin")
	if catppuccin == nil {
		t.Errorf("Catppuccin theme is nil")
	}
	
	gruvbox := GetTheme("gruvbox")
	if gruvbox == nil {
		t.Errorf("Gruvbox theme is nil")
	}
	
	monokai := GetTheme("monokai")
	if monokai == nil {
		t.Errorf("Monokai theme is nil")
	}
	
	// Test switching theme
	originalTheme := CurrentThemeName()
	
	err := SetTheme("gruvbox")
	if err != nil {
		t.Errorf("Failed to set theme to gruvbox: %v", err)
	}
	
	if CurrentThemeName() != "gruvbox" {
		t.Errorf("Theme not properly switched to gruvbox")
	}
	
	err = SetTheme("monokai")
	if err != nil {
		t.Errorf("Failed to set theme to monokai: %v", err)
	}
	
	if CurrentThemeName() != "monokai" {
		t.Errorf("Theme not properly switched to monokai")
	}
	
	// Switch back to original theme
	_ = SetTheme(originalTheme)
}

func TestAdaptiveColorDarkLightSwitching(t *testing.T) {
	origDark := HasDarkBackground()
	t.Cleanup(func() {
		SetHasDarkBackground(origDark)
	})

	c := AdaptiveColor{
		Light: "#112233",
		Dark:  "#aabbcc",
	}

	// Verify AdaptiveColor satisfies color.Color.
	var _ color.Color = c

	SetHasDarkBackground(true)
	if !HasDarkBackground() {
		t.Fatalf("expected HasDarkBackground() to be true")
	}
	if got := c.Hex(); got != "#aabbcc" {
		t.Errorf("dark Hex() = %q, want %q", got, "#aabbcc")
	}
	r, g, b, a := c.RGBA()
	if uint8(r>>8) != 0xaa || uint8(g>>8) != 0xbb || uint8(b>>8) != 0xcc || a == 0 {
		t.Errorf("dark RGBA() = (%x,%x,%x,%x), want (#aa,#bb,#cc,non-zero)", r>>8, g>>8, b>>8, a)
	}

	SetHasDarkBackground(false)
	if HasDarkBackground() {
		t.Fatalf("expected HasDarkBackground() to be false")
	}
	if got := c.Hex(); got != "#112233" {
		t.Errorf("light Hex() = %q, want %q", got, "#112233")
	}
	r, g, b, a = c.RGBA()
	if uint8(r>>8) != 0x11 || uint8(g>>8) != 0x22 || uint8(b>>8) != 0x33 || a == 0 {
		t.Errorf("light RGBA() = (%x,%x,%x,%x), want (#11,#22,#33,non-zero)", r>>8, g>>8, b>>8, a)
	}

	// Empty AdaptiveColor must not panic.
	var empty AdaptiveColor
	_, _, _, _ = empty.RGBA()
}

func TestOnThemeChangeCallbacks(t *testing.T) {
	origTheme := CurrentThemeName()
	origDark := HasDarkBackground()
	t.Cleanup(func() {
		SetHasDarkBackground(origDark)
		_ = SetTheme(origTheme)
	})

	var calls atomic.Int32
	OnThemeChange(func() {
		calls.Add(1)
	})

	if err := SetTheme("nonexistent-theme-xyz"); err == nil {
		t.Fatalf("expected error for nonexistent theme")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("expected 0 callbacks on failed SetTheme, got %d", got)
	}

	if err := SetTheme("dracula"); err != nil {
		t.Fatalf("SetTheme(dracula) failed: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 callback after SetTheme, got %d", got)
	}

	SetHasDarkBackground(!origDark)
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 callbacks after SetHasDarkBackground, got %d", got)
	}
}

func TestAdaptiveColorConcurrentAccess(t *testing.T) {
	origDark := HasDarkBackground()
	t.Cleanup(func() {
		SetHasDarkBackground(origDark)
	})

	c := AdaptiveColor{Light: "#ffffff", Dark: "#000000"}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 20 {
				if id%2 == 0 {
					SetHasDarkBackground(j%2 == 0)
				} else {
					_ = c.Hex()
					_, _, _, _ = c.RGBA()
				}
			}
		}(i)
	}
	wg.Wait()
}
