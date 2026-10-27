package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// registryFile is the on-disk shape of the installed app registry.
type registryFile struct {
	Apps []AppInfo `json:"apps"`
}

// Registry is the local list of installed apps. The launcher reads it; the
// installer will write it once installation lands.
type Registry struct {
	apps []AppInfo
}

// NewRegistry creates a registry from apps. Entries with no ID or name are
// skipped; entries with an unknown status are kept as damaged.
func NewRegistry(apps ...AppInfo) *Registry {
	r := &Registry{apps: make([]AppInfo, 0, len(apps))}
	for _, app := range apps {
		app.ID = strings.TrimSpace(app.ID)
		app.Name = strings.TrimSpace(app.Name)
		if app.ID == "" || app.Name == "" {
			continue
		}
		if !app.Status.Valid() {
			app.Status = StatusCorrupt
		}
		// The registry lists installed apps only; a not-installed entry is
		// ignored rather than shown in the launcher.
		if app.Status == StatusNotInstalled {
			continue
		}
		r.apps = append(r.apps, app)
	}
	return r
}

// LoadRegistry reads the installed app registry. A missing file is an empty
// registry, not an error; unreadable or invalid JSON returns an empty
// registry and the error, so the caller can log it and keep the app usable.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewRegistry(), nil
	}
	if err != nil {
		return NewRegistry(), fmt.Errorf("store: read registry: %w", err)
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return NewRegistry(), fmt.Errorf("store: parse registry: %w", err)
	}
	return NewRegistry(file.Apps...), nil
}

// Apps returns a copy of the registered apps.
func (r *Registry) Apps() []AppInfo {
	if r == nil {
		return nil
	}
	return append([]AppInfo(nil), r.apps...)
}

// Len returns the number of registered apps.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.apps)
}
