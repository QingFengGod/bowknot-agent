package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/bowknot/bowknot-agent/internal/ui"
)

// settings is the content of settings.json in the data directory.
type settings struct {
	Theme string `json:"theme"`
}

// loadTheme reads the appearance saved at path. A missing, unreadable or
// malformed file, and an unknown theme, fall back to light without stopping
// the app.
func loadTheme(path string) ui.Mode {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("app: read settings %s: %v; using light theme", path, err)
		return ui.ModeLight
	}
	var s settings
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("app: parse settings %s: %v; using light theme", path, err)
		return ui.ModeLight
	}
	switch ui.Mode(s.Theme) {
	case ui.ModeLight, ui.ModeDark:
		return ui.Mode(s.Theme)
	default:
		log.Printf("app: unknown theme %q in %s; using light theme", s.Theme, path)
		return ui.ModeLight
	}
}

// saveTheme writes mode to path. It writes a temporary file first and renames
// it, so that a half-written settings.json never replaces a good one.
func saveTheme(path string, mode ui.Mode) error {
	data, err := json.Marshal(settings{Theme: string(mode)})
	if err != nil {
		return fmt.Errorf("app: encode settings: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("app: create settings directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("app: write settings %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows refuses the rename when another process holds the target;
		// one retry covers the momentary case, then the temporary file goes.
		if retry := os.Rename(tmp, path); retry != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("app: replace settings %s: %w", path, retry)
		}
	}
	return nil
}
