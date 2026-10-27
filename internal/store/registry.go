package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// registryFile is the on-disk shape of the installed app registry.
type registryFile struct {
	Apps []AppInfo `json:"apps"`
}

// Registry is the local list of installed apps. It is safe for concurrent use:
// the installer writes it from a worker goroutine while the launcher reads it.
type Registry struct {
	mu   sync.RWMutex
	path string
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
		r := NewRegistry()
		r.path = path
		return r, nil
	}
	if err != nil {
		return NewRegistry(), fmt.Errorf("store: read registry: %w", err)
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return NewRegistry(), fmt.Errorf("store: parse registry: %w", err)
	}
	r := NewRegistry(file.Apps...)
	r.path = path
	return r, nil
}

// SaveRegistry writes registry to path atomically.
func SaveRegistry(path string, registry *Registry) error {
	if registry == nil {
		return fmt.Errorf("store: save registry: nil registry")
	}
	return registry.SaveTo(path)
}

// Save writes the registry to the path it was loaded from.
func (r *Registry) Save() error {
	r.mu.RLock()
	path := r.path
	r.mu.RUnlock()
	if path == "" {
		return fmt.Errorf("store: save registry: no path")
	}
	return r.SaveTo(path)
}

// SaveTo writes the registry to path, replacing the file atomically.
func (r *Registry) SaveTo(path string) error {
	r.mu.RLock()
	file := registryFile{Apps: append([]AppInfo(nil), r.apps...)}
	r.mu.RUnlock()
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode registry: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	r.mu.Lock()
	r.path = path
	r.mu.Unlock()
	return nil
}

// writeFileAtomic writes data to a temporary file in the destination
// directory and renames it into place.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("store: create registry directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".registry-*.tmp")
	if err != nil {
		return fmt.Errorf("store: create registry temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("store: write registry temp file: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return fmt.Errorf("store: chmod registry temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("store: close registry temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("store: replace registry: %w", err)
	}
	return nil
}

// Apps returns a copy of the registered apps.
func (r *Registry) Apps() []AppInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]AppInfo(nil), r.apps...)
}

// Len returns the number of registered apps.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.apps)
}

// Get returns the app with the ID.
func (r *Registry) Get(id string) (AppInfo, bool) {
	if r == nil {
		return AppInfo{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, app := range r.apps {
		if app.ID == id {
			return app, true
		}
	}
	return AppInfo{}, false
}

// Upsert adds app or replaces the app with the same ID.
func (r *Registry) Upsert(app AppInfo) {
	if r == nil {
		return
	}
	app.ID = strings.TrimSpace(app.ID)
	app.Name = strings.TrimSpace(app.Name)
	if app.ID == "" || app.Name == "" {
		return
	}
	if !app.Status.Valid() {
		app.Status = StatusCorrupt
	}
	if app.Status == StatusNotInstalled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.apps {
		if r.apps[i].ID == app.ID {
			r.apps[i] = app
			return
		}
	}
	r.apps = append(r.apps, app)
}

// Remove drops the app with the ID and reports whether it was present.
func (r *Registry) Remove(id string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, app := range r.apps {
		if app.ID == id {
			r.apps = append(r.apps[:i], r.apps[i+1:]...)
			return true
		}
	}
	return false
}

// SetStatus updates the status of an app and reports whether it was present.
func (r *Registry) SetStatus(id string, status Status) bool {
	if r == nil || !status.Valid() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.apps {
		if r.apps[i].ID == id {
			r.apps[i].Status = status
			return true
		}
	}
	return false
}

// Touch records the last launch time of an app.
func (r *Registry) Touch(id string, unix int64) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.apps {
		if r.apps[i].ID == id {
			r.apps[i].LastOpened = unix
			return true
		}
	}
	return false
}

// Normalize maps transient in-memory statuses to installed. It is called once
// at startup so that a registry left behind by a crash shows no fake running
// state.
func (r *Registry) Normalize() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	for i := range r.apps {
		switch r.apps[i].Status {
		case StatusInstalling, StatusRunning, StatusUpdating:
			r.apps[i].Status = StatusInstalled
			changed = true
		}
	}
	return changed
}

// VerifyPrograms checks that each registered app still has a readable manifest
// and marks the missing or invalid ones corrupt. It reports whether the
// registry changed.
func (r *Registry) VerifyPrograms(appsDir, shellVersion string) bool {
	if r == nil {
		return false
	}
	changed := false
	for _, app := range r.Apps() {
		if _, err := ReadManifest(filepath.Join(appsDir, app.ID), shellVersion); err != nil {
			if app.Status != StatusCorrupt {
				r.SetStatus(app.ID, StatusCorrupt)
				changed = true
			}
		}
	}
	return changed
}
