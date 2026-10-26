package ui

import mygoui "github.com/egoist/mygo/ui"

// Mode is the appearance the app shows: light or dark.
type Mode string

// Appearance modes.
const (
	ModeLight Mode = "light"
	ModeDark  Mode = "dark"
)

// Toggle returns the other mode.
func (m Mode) Toggle() Mode {
	if m == ModeDark {
		return ModeLight
	}
	return ModeDark
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
	return mygoui.RGBA(249, 115, 22, 0.3)
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
