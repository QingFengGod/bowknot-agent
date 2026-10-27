package ui

import (
	"embed"
	"fmt"

	mygoui "github.com/egoist/mygo/ui"
)

//go:embed icons/*.svg
var iconFiles embed.FS

// Icons of the shell and the title bar, parsed once at startup.
var (
	StoreIcon    = mustIcon("store.svg")
	AgentIcon    = mustIcon("agent.svg")
	CreationIcon = mustIcon("creation.svg")
	SettingsIcon = mustIcon("settings.svg")
	SunIcon      = mustIcon("sun.svg")
	MoonIcon     = mustIcon("moon.svg")
	AutoIcon     = mustIcon("auto.svg")
)

func mustIcon(name string) *mygoui.SVG {
	data, err := iconFiles.ReadFile("icons/" + name)
	if err != nil {
		panic(fmt.Sprintf("ui: read icon %s: %v", name, err))
	}
	return mygoui.MustParseSVG(data)
}
