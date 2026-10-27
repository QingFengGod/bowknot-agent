package ui

import (
	mygo "github.com/egoist/mygo"
	mygoui "github.com/egoist/mygo/ui"
)

// Mode is the appearance the app draws: light or dark.
type Mode string

// Appearance modes.
const (
	ModeLight Mode = "light"
	ModeDark  Mode = "dark"
)

// Preference is the appearance the user chose: light, dark, or follow the
// system. The default is system.
type Preference string

// Appearance preferences.
const (
	PreferenceLight  Preference = "light"
	PreferenceDark   Preference = "dark"
	PreferenceSystem Preference = "system"
)

// Next returns the preference one step along light → dark → system. An
// unknown preference is treated as system.
func (p Preference) Next() Preference {
	switch p {
	case PreferenceLight:
		return PreferenceDark
	case PreferenceDark:
		return PreferenceSystem
	default:
		return PreferenceLight
	}
}

// Source returns the mygo appearance source of the preference.
func (p Preference) Source() mygo.ThemeSource {
	switch p {
	case PreferenceLight:
		return mygo.ThemeLight
	case PreferenceDark:
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}

// Resolve returns the appearance to draw for preference. While following the
// system, systemDark is the desktop's appearance.
func Resolve(p Preference, systemDark bool) Mode {
	switch p {
	case PreferenceLight:
		return ModeLight
	case PreferenceDark:
		return ModeDark
	default:
		if systemDark {
			return ModeDark
		}
		return ModeLight
	}
}

// Theme returns the tokens of the appearance of mode. Both themes are built
// once; callers must not modify the result.
func Theme(mode Mode) *mygoui.Theme {
	if mode == ModeDark {
		return darkTheme
	}
	return lightTheme
}

// BackgroundHex returns the window background of mode, for
// mygo.Window.SetBackgroundColor, so that no white flash shows while the
// window repaints.
func BackgroundHex(mode Mode) string {
	if mode == ModeDark {
		return "#18181b"
	}
	return "#ffffff"
}

// AccentTint returns the accent color at 30% opacity, for the background of
// the selected navigation item.
func AccentTint() mygoui.Color {
	return mygoui.RGBA(249, 115, 22, 0.2)
}

// SuccessColor returns the color of a healthy state, such as an online
// gateway.
func SuccessColor(mode Mode) mygoui.Color {
	if mode == ModeDark {
		return mygoui.Hex("#4ade80")
	}
	return mygoui.Hex("#16a34a")
}

// DangerColor returns the color of a failed state, such as an offline
// gateway.
func DangerColor(mode Mode) mygoui.Color {
	if mode == ModeDark {
		return mygoui.Hex("#f87171")
	}
	return mygoui.Hex("#dc2626")
}

var (
	lightTheme = buildTheme(ModeLight)
	darkTheme  = buildTheme(ModeDark)
)

func buildTheme(mode Mode) *mygoui.Theme {
	dark := mode == ModeDark
	t := mygoui.LightTheme()
	if dark {
		t = mygoui.DarkTheme()
	}
	t.Dark = dark
	if dark {
		t.Background = mygoui.Hex("#18181b")
		t.Surface = mygoui.Hex("#27272a")
		t.SurfaceHover = mygoui.Hex("#3f3f46")
		t.SurfacePressed = mygoui.Hex("#52525b")
		t.Border = mygoui.Hex("#3f3f46")
		t.Text = mygoui.Hex("#f4f4f5")
		t.TextMuted = mygoui.Hex("#a1a1aa")
		t.Accent = mygoui.Hex("#f97316")
		t.AccentHover = mygoui.Hex("#fb923c")
		t.AccentPressed = mygoui.Hex("#ea580c")
		t.Selection = mygoui.RGBA(249, 115, 22, 0.4)
		t.Focus = mygoui.RGBA(251, 146, 60, 0.6)
	} else {
		t.Background = mygoui.Hex("#ffffff")
		t.Surface = mygoui.Hex("#fafafa")
		t.SurfaceHover = mygoui.Hex("#f4f4f5")
		t.SurfacePressed = mygoui.Hex("#e4e4e7")
		t.Border = mygoui.Hex("#e4e4e7")
		t.Text = mygoui.Hex("#18181b")
		t.TextMuted = mygoui.Hex("#71717a")
		t.Accent = mygoui.Hex("#f97316")
		t.AccentHover = mygoui.Hex("#ea580c")
		t.AccentPressed = mygoui.Hex("#c2410c")
		t.Selection = mygoui.RGBA(249, 115, 22, 0.25)
		t.Focus = mygoui.RGBA(249, 115, 22, 0.55)
	}
	t.AccentText = mygoui.Hex("#ffffff")
	t.Radius = 8
	t.Spacing = 4
	t.FontSize = 14
	return t
}
