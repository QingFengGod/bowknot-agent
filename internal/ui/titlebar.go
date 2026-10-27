package ui

import (
	mygoui "github.com/egoist/mygo/ui"
)

// Metrics and colors of the title bar.
const (
	// TitleBarHeight is the height of the title bar, in DIPs.
	TitleBarHeight = 44

	logoSize        = 20
	lightSize       = 12
	lightGap        = 8
	themeButtonSize = 28

	closeLight    = "#ff5f57"
	minimizeLight = "#febc2e"
	maximizeLight = "#28c840"
)

// TitleBarActions are what the title bar shows and what its buttons do.
type TitleBarActions struct {
	// Preference is the appearance the user chose, which decides the theme
	// button's icon.
	Preference Preference
	// Focused is whether the window is focused; the traffic lights are gray
	// while it is not.
	Focused       bool
	OnToggleTheme func()
	OnMinimize    func()
	OnMaximize    func()
	OnClose       func()
}

// TitleBar draws the app's title bar: the logo and the app name on the
// left, the theme button and the traffic lights on the right. The blank
// space drags the window, and a double click maximizes or restores it.
func TitleBar(c *mygoui.Context, a TitleBarActions) mygoui.Element {
	t := c.Theme()
	bar := mygoui.Row(c).FillWidth().Height(TitleBarHeight).Shrink(0).
		AlignItems(mygoui.Center).Padding(0, 16).Gap(8).
		Background(t.Background).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
		DragWindow()
	bar.Children(func() {
		logo(c, t)
		mygoui.Text(c, "Bowknot Agent").FontSize(13).Bold().TextColor(t.Text).SingleLine()
		mygoui.Spacer(c)
		themeButton(c, t, a)
		trafficLights(c, t, a)
	})
	return bar
}

func logo(c *mygoui.Context, t *mygoui.Theme) {
	mark := mygoui.Box(c).Size(logoSize, logoSize).Radius(4).Shrink(0).
		Background(t.Accent).Center()
	mark.Children(func() {
		mygoui.Text(c, "B").FontSize(11).Bold().TextColor(t.AccentText)
	})
}

func themeButton(c *mygoui.Context, t *mygoui.Theme, a TitleBarActions) {
	var icon *mygoui.SVG
	var name string
	switch a.Preference {
	case PreferenceLight:
		icon, name = MoonIcon, "切换到暗色主题"
	case PreferenceDark:
		icon, name = SunIcon, "切换到跟随系统主题"
	default:
		icon, name = AutoIcon, "切换到亮色主题"
	}
	b := mygoui.ButtonBase(c).Label("切换主题").Tooltip(name).Shrink(0).
		Size(themeButtonSize, themeButtonSize).Radius(t.Radius / 2).Center().
		TextColor(t.TextMuted)
	if b.Pressed() {
		b.Background(t.SurfacePressed)
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Icon(c, icon).Size(16, 16)
	})
	if b.Clicked() && a.OnToggleTheme != nil {
		a.OnToggleTheme()
	}
}

func trafficLights(c *mygoui.Context, t *mygoui.Theme, a TitleBarActions) {
	closeColor, minimizeColor, maximizeColor := mygoui.Hex(closeLight), mygoui.Hex(minimizeLight), mygoui.Hex(maximizeLight)
	if !a.Focused {
		closeColor, minimizeColor, maximizeColor = t.Border, t.Border, t.Border
	}
	row := mygoui.Row(c).Gap(lightGap).Shrink(0).Margin(0, 0, 0, 4)
	row.Children(func() {
		trafficLight(c, "最小化", minimizeColor, a.OnMinimize)
		trafficLight(c, "最大化", maximizeColor, a.OnMaximize)
		trafficLight(c, "关闭", closeColor, a.OnClose)
	})
}

// trafficLight draws one light, plain and without a symbol.
func trafficLight(c *mygoui.Context, label string, color mygoui.Color, action func()) {
	b := mygoui.ButtonBase(c).Label(label).Shrink(0).
		Size(lightSize, lightSize).Radius(lightSize / 2).Background(color).Center()
	if b.Clicked() && action != nil {
		action()
	}
}
