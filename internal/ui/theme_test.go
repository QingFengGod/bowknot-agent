package ui

import (
	"testing"

	mygo "github.com/egoist/mygo"
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

func TestStatusColors(t *testing.T) {
	tests := []struct {
		name            string
		mode            Mode
		success, danger string
	}{
		{"light", ModeLight, "#16a34a", "#dc2626"},
		{"dark", ModeDark, "#4ade80", "#f87171"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SuccessColor(tt.mode); got != mygoui.Hex(tt.success) {
				t.Errorf("SuccessColor(%q) = %v, want %s", tt.mode, got, tt.success)
			}
			if got := DangerColor(tt.mode); got != mygoui.Hex(tt.danger) {
				t.Errorf("DangerColor(%q) = %v, want %s", tt.mode, got, tt.danger)
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

func TestPreferenceNext(t *testing.T) {
	tests := []struct {
		from Preference
		want Preference
	}{
		{PreferenceLight, PreferenceDark},
		{PreferenceDark, PreferenceSystem},
		{PreferenceSystem, PreferenceLight},
		{Preference("unknown"), PreferenceLight},
	}
	for _, tt := range tests {
		if got := tt.from.Next(); got != tt.want {
			t.Errorf("%q.Next() = %q, want %q", tt.from, got, tt.want)
		}
	}
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name       string
		preference Preference
		systemDark bool
		want       Mode
	}{
		{"light with light system", PreferenceLight, false, ModeLight},
		{"light with dark system", PreferenceLight, true, ModeLight},
		{"dark with light system", PreferenceDark, false, ModeDark},
		{"dark with dark system", PreferenceDark, true, ModeDark},
		{"system with light desktop", PreferenceSystem, false, ModeLight},
		{"system with dark desktop", PreferenceSystem, true, ModeDark},
		{"unknown follows the desktop", Preference("unknown"), true, ModeDark},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.preference, tt.systemDark); got != tt.want {
				t.Errorf("Resolve(%q, %v) = %q, want %q", tt.preference, tt.systemDark, got, tt.want)
			}
		})
	}
}

func TestPreferenceSource(t *testing.T) {
	tests := []struct {
		preference Preference
		want       mygo.ThemeSource
	}{
		{PreferenceLight, mygo.ThemeLight},
		{PreferenceDark, mygo.ThemeDark},
		{PreferenceSystem, mygo.ThemeSystem},
		{Preference("unknown"), mygo.ThemeSystem},
	}
	for _, tt := range tests {
		if got := tt.preference.Source(); got != tt.want {
			t.Errorf("%q.Source() = %q, want %q", tt.preference, got, tt.want)
		}
	}
}
