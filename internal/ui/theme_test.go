package ui

import (
	"testing"

	mygoui "github.com/egoist/mygo/ui"
)

func TestThemeTokens(t *testing.T) {
	tests := []struct {
		name       string
		mode       Mode
		dark       bool
		background string
		surface    string
		text       string
		accent     string
	}{
		{"light", ModeLight, false, "#ffffff", "#fafafa", "#18181b", "#f97316"},
		{"dark", ModeDark, true, "#18181b", "#27272a", "#f4f4f5", "#f97316"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			th := Theme(tt.mode)
			if th.Dark != tt.dark {
				t.Errorf("Dark = %v, want %v", th.Dark, tt.dark)
			}
			if th.Background != mygoui.Hex(tt.background) {
				t.Errorf("Background = %v, want %s", th.Background, tt.background)
			}
			if th.Surface != mygoui.Hex(tt.surface) {
				t.Errorf("Surface = %v, want %s", th.Surface, tt.surface)
			}
			if th.Text != mygoui.Hex(tt.text) {
				t.Errorf("Text = %v, want %s", th.Text, tt.text)
			}
			if th.Accent != mygoui.Hex(tt.accent) {
				t.Errorf("Accent = %v, want %s", th.Accent, tt.accent)
			}
			if th.AccentText != mygoui.Hex("#ffffff") {
				t.Errorf("AccentText = %v, want #ffffff", th.AccentText)
			}
			if th.Radius != 8 || th.Spacing != 4 || th.FontSize != 14 {
				t.Errorf("metrics = (%v, %v, %v), want (8, 4, 14)", th.Radius, th.Spacing, th.FontSize)
			}
		})
	}
}

func TestBackgroundHex(t *testing.T) {
	if got := BackgroundHex(ModeLight); got != "#ffffff" {
		t.Errorf("light background = %q, want #ffffff", got)
	}
	if got := BackgroundHex(ModeDark); got != "#18181b" {
		t.Errorf("dark background = %q, want #18181b", got)
	}
}

func TestModeToggle(t *testing.T) {
	if got := ModeLight.Toggle(); got != ModeDark {
		t.Errorf("ModeLight.Toggle() = %q, want dark", got)
	}
	if got := ModeDark.Toggle(); got != ModeLight {
		t.Errorf("ModeDark.Toggle() = %q, want light", got)
	}
}
