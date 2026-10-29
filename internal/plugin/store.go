package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Registry layout and audit limits.
const (
	// RegistryFileName is the installed plugin registry inside the plugins
	// directory.
	RegistryFileName = "registry.json"
	// AuditFileName is the append-only audit log inside the plugins directory.
	// Plugins are declarative and cannot write anywhere, so they cannot touch
	// this file either.
	AuditFileName = "audit.jsonl"
	// stagingDirName holds the temporary directories of one install.
	stagingDirName = ".staging"
	// MaxAuditBytes rotates the audit log when it grows past this size.
	MaxAuditBytes = 512 << 10
	// keepAuditLines is how many lines a rotated audit log keeps.
	keepAuditLines = 500
)

// Registry schema of registry.json.
const RegistrySchema = 1

// Store errors.
var (
	// ErrDowngrade rejects installing a version older than the installed one.
	ErrDowngrade = errors.New("插件版本降级被拒绝")
	// ErrNotFound reports an unknown or uninstalled plugin.
	ErrNotFound = errors.New("插件未安装")
)

// Entry is one installed plugin in the registry.
type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Skills      []string `json:"skills"`
	Tools       []string `json:"tools"`
	Icon        string   `json:"icon,omitempty"`
	// SHA256 is the digest of the installed files, computed at install time.
	SHA256      string `json:"sha256"`
	InstalledAt int64  `json:"installed_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// registryFile is the on-disk shape of registry.json.
type registryFile struct {
	Schema  int     `json:"schema"`
	Plugins []Entry `json:"plugins"`
}

// Item is one row of the installed list: the registry entry plus whether the
// plugin directory still validates.
type Item struct {
	Entry Entry
	Valid bool
}

// Store keeps the installed plugins under one directory. Every method is safe
// to call from any goroutine.
type Store struct {
	dir          string
	shellVersion string
	now          func() time.Time

	mu      sync.Mutex
	entries []Entry
}

// NewStore returns a store over the plugins directory. An empty directory
// keeps the registry in memory only, which the tests use.
func NewStore(dir, shellVersion string) *Store {
	return &Store{dir: dir, shellVersion: shellVersion, now: time.Now}
}

// Dir returns the plugins directory, or "" when the store runs in memory.
func (s *Store) Dir() string { return s.dir }

// PluginDir returns the install directory of one plugin ID.
func (s *Store) PluginDir(id string) string {
	if s.dir == "" {
		return ""
	}
	return filepath.Join(s.dir, id)
}

// Load reads registry.json. A missing file is an empty registry; an unreadable
// or corrupt file is reported and keeps the registry empty rather than
// guessing at half-written entries.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	if s.dir == "" {
		return nil
	}
	path := filepath.Join(s.dir, RegistryFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("plugin: read registry: %w", err)
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("plugin: parse registry: %w", err)
	}
	if file.Schema != RegistrySchema {
		return fmt.Errorf("plugin: registry schema %d is not supported", file.Schema)
	}
	entries := make([]Entry, 0, len(file.Plugins))
	for _, e := range file.Plugins {
		if e.ID == "" || !validEntry(e) {
			log.Printf("plugin: skip invalid registry entry %q", e.ID)
			continue
		}
		entries = append(entries, e)
	}
	s.entries = entries
	return nil
}

func validEntry(e Entry) bool {
	return pluginIDPattern.MatchString(e.ID) &&
		versionPattern.MatchString(e.Version) &&
		len(e.Skills) > 0 && len(e.Skills) <= MaxSkills
}

// Save writes the registry atomically.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("plugin: create plugins directory: %w", err)
	}
	file := registryFile{Schema: RegistrySchema, Plugins: append([]Entry(nil), s.entries...)}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("plugin: encode registry: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(s.dir, RegistryFileName)
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("plugin: save registry: %w", err)
	}
	return nil
}

// writeFileAtomic writes data through a temporary file in the target
// directory and replaces the target with it.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("set file permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	temp = ""
	return nil
}

// List returns every registry entry with its directory integrity. A directory
// that no longer validates stays listed as invalid so the user can re-import
// it to repair, and the per-agent enable lists can prune it.
func (s *Store) List() []Item {
	s.mu.Lock()
	entries := append([]Entry(nil), s.entries...)
	s.mu.Unlock()
	out := make([]Item, 0, len(entries))
	for _, e := range entries {
		out = append(out, Item{Entry: e, Valid: s.Valid(e.ID)})
	}
	return out
}

// Get returns one registry entry.
func (s *Store) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Valid reports whether the plugin directory still holds a readable manifest
// with its files intact.
func (s *Store) Valid(id string) bool {
	_, err := s.Manifest(id)
	return err == nil
}

// Manifest reads and validates the manifest of one installed plugin.
func (s *Store) Manifest(id string) (*Manifest, error) {
	dir := s.PluginDir(id)
	if dir == "" {
		return nil, ErrNotFound
	}
	return ReadManifest(dir, s.shellVersion)
}

// Inspect validates a source without installing it: the package or directory
// is copied into a staging directory, the manifest and the layout are checked,
// and the staging directory is removed again. It returns the manifest of the
// source so the caller can ask before overwriting an installed plugin.
func (s *Store) Inspect(path string, directory bool) (*Manifest, error) {
	staging, done, err := s.stage(path, directory)
	if err != nil {
		return nil, err
	}
	defer done()
	return ReadManifest(staging, s.shellVersion)
}

// Install validates and installs a plugin package or directory, replacing an
// installed plugin of the same ID when its version is the same or newer. A
// downgrade is rejected. The caller confirms the overwrite before calling.
func (s *Store) Install(path string, directory bool) (Entry, error) {
	if strings.TrimSpace(path) == "" {
		return Entry{}, errors.New("未选择导入来源")
	}
	staging, done, err := s.stage(path, directory)
	if err != nil {
		return Entry{}, err
	}
	defer done()

	manifest, err := ReadManifest(staging, s.shellVersion)
	if err != nil {
		return Entry{}, err
	}
	if err := validateLayout(staging, manifest); err != nil {
		return Entry{}, err
	}
	digest, err := hashTree(staging)
	if err != nil {
		return Entry{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.findLocked(manifest.ID)
	if exists && CompareVersions(manifest.Version, previous.Version) < 0 {
		return Entry{}, fmt.Errorf("%w：已安装 %s，导入的是 %s", ErrDowngrade, previous.Version, manifest.Version)
	}
	if s.dir == "" {
		return Entry{}, nil
	}
	final := filepath.Join(s.dir, manifest.ID)
	backup := ""
	if exists {
		backup = final + ".backup"
		if err := os.Rename(final, backup); err != nil {
			return Entry{}, fmt.Errorf("plugin: back up installed plugin: %w", err)
		}
	}
	if err := os.Rename(staging, final); err != nil {
		if backup != "" {
			_ = os.Rename(backup, final)
		}
		return Entry{}, fmt.Errorf("plugin: install plugin directory: %w", err)
	}
	now := s.now().Unix()
	entry := Entry{
		ID:          manifest.ID,
		Name:        manifest.Name,
		Version:     manifest.Version,
		Description: manifest.Description,
		Author:      manifest.Author,
		Skills:      append([]string(nil), manifest.Skills...),
		Tools:       append([]string(nil), manifest.Tools...),
		Icon:        manifest.Icon,
		SHA256:      digest,
		InstalledAt: now,
		UpdatedAt:   now,
	}
	if exists {
		entry.InstalledAt = previous.InstalledAt
	}
	s.upsertLocked(entry)
	if err := s.saveLocked(); err != nil {
		// Roll the install back so the registry never disagrees with the disk.
		_ = os.RemoveAll(final)
		if exists {
			_ = os.Rename(backup, final)
		}
		s.replaceLocked(previous, exists)
		return Entry{}, err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	kind := "导入"
	if exists {
		kind = "更新"
	}
	s.recordLocked("install", entry.ID, fmt.Sprintf("%s %s（来源：%s）", kind, entry.Version, sourceKind(directory)))
	return entry, nil
}

// Uninstall removes one plugin directory and its registry entry. The caller
// removes the plugin from the per-agent enable lists first or afterwards; the
// audit log and the historical call records are never touched.
func (s *Store) Uninstall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, exists := s.findLocked(id)
	if !exists {
		return ErrNotFound
	}
	if s.dir != "" {
		if err := os.RemoveAll(filepath.Join(s.dir, id)); err != nil {
			return fmt.Errorf("plugin: delete plugin directory: %w", err)
		}
	}
	s.removeLocked(id)
	if err := s.saveLocked(); err != nil {
		s.upsertLocked(previous)
		return err
	}
	s.recordLocked("uninstall", id, "卸载 "+previous.Version)
	return nil
}

// CleanStaging removes leftovers of interrupted installs. Failures are left
// for the next start rather than blocking startup.
func (s *Store) CleanStaging() {
	if s.dir == "" {
		return
	}
	staging := filepath.Join(s.dir, stagingDirName)
	entries, err := os.ReadDir(staging)
	if err != nil {
		return
	}
	for _, entry := range entries {
		_ = os.RemoveAll(filepath.Join(staging, entry.Name()))
	}
}

// stage copies the source into a fresh staging directory and returns the
// directory plus the cleanup function.
func (s *Store) stage(path string, directory bool) (string, func(), error) {
	if s.dir == "" {
		return "", func() {}, errors.New("plugin: store directory is not configured")
	}
	stagingRoot := filepath.Join(s.dir, stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("plugin: create staging root: %w", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, "install-")
	if err != nil {
		return "", func() {}, fmt.Errorf("plugin: create staging: %w", err)
	}
	done := func() { _ = os.RemoveAll(staging) }
	if directory {
		err = CopyDirectory(path, staging)
	} else {
		f, openErr := os.Open(path)
		if openErr != nil {
			done()
			return "", done, fmt.Errorf("plugin: 无法读取安装包：%w", openErr)
		}
		defer f.Close()
		err = ExtractPackage(f, staging)
	}
	if err != nil {
		done()
		return "", done, err
	}
	return staging, done, nil
}

func (s *Store) findLocked(id string) (Entry, bool) {
	for _, e := range s.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

func (s *Store) upsertLocked(entry Entry) {
	s.replaceLocked(entry, true)
}

func (s *Store) replaceLocked(entry Entry, exists bool) {
	if exists {
		for i := range s.entries {
			if s.entries[i].ID == entry.ID {
				s.entries[i] = entry
				return
			}
		}
	}
	s.entries = append(s.entries, entry)
}

func (s *Store) removeLocked(id string) {
	for i := range s.entries {
		if s.entries[i].ID == id {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func sourceKind(directory bool) string {
	if directory {
		return "目录"
	}
	return "安装包"
}

// Record appends one audit line. The app records plugin enables, disables and
// tool call summaries through it; imports and uninstalls record themselves.
func (s *Store) Record(action, pluginID, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked(action, pluginID, detail)
}

// auditLine is one line of audit.jsonl.
type auditLine struct {
	At     int64  `json:"at"`
	Action string `json:"action"`
	Plugin string `json:"plugin"`
	Detail string `json:"detail,omitempty"`
}

func (s *Store) recordLocked(action, pluginID, detail string) {
	if s.dir == "" {
		return
	}
	line, err := json.Marshal(auditLine{
		At:     s.now().Unix(),
		Action: action,
		Plugin: pluginID,
		Detail: detail,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		log.Printf("plugin: create plugins directory: %v", err)
		return
	}
	path := filepath.Join(s.dir, AuditFileName)
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > MaxAuditBytes {
		s.rotateAuditLocked(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("plugin: open audit log: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		log.Printf("plugin: write audit log: %v", err)
	}
}

// rotateAuditLocked keeps the newest lines when the log grows too large.
func (s *Store) rotateAuditLocked(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > keepAuditLines {
		lines = lines[len(lines)-keepAuditLines:]
	}
	if err := writeFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		log.Printf("plugin: rotate audit log: %v", err)
	}
}
