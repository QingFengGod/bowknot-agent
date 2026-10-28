package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	taskExt       = ".json"
	storeDirMode  = 0o700
	storeFileMode = 0o600
)

// ErrGroupNotFound is returned when a group ID is unknown.
var ErrGroupNotFound = errors.New("任务组不存在")

// Summary is one row of the task group list.
type Summary struct {
	ID             string
	ConversationID string
	MessageIndex   int
	Status         GroupStatus
	Tasks          int
	UpdatedAt      int64
}

// Store keeps task groups, one JSON file each. Every method is safe to call
// from any goroutine; the view reads snapshots only.
type Store struct {
	mu     sync.Mutex
	dir    string
	now    func() time.Time
	groups map[string]*Group
}

// NewStore returns a store over dir. An empty dir keeps groups in memory,
// which the tests use; Load then does nothing.
func NewStore(dir string) *Store {
	return &Store{
		dir:    dir,
		now:    time.Now,
		groups: map[string]*Group{},
	}
}

// Dir returns the directory the store loads and saves.
func (s *Store) Dir() string { return s.dir }

// Load reads every valid group file, then recovers groups a crash left
// running. Malformed, oversized and unsupported files are logged and
// skipped, never deleted.
func (s *Store) Load() error {
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, storeDirMode); err != nil {
		return fmt.Errorf("task: create task directory: %w", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("task: list tasks: %w", err)
	}
	loaded := make(map[string]*Group, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), taskExt) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			log.Printf("task: skip group %s: %v", entry.Name(), infoErr)
			continue
		}
		if info.Size() > MaxTaskFileBytes {
			log.Printf("task: skip group %s: file exceeds %d bytes", entry.Name(), MaxTaskFileBytes)
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			log.Printf("task: skip group %s: %v", entry.Name(), readErr)
			continue
		}
		var g Group
		if unmarshalErr := json.Unmarshal(data, &g); unmarshalErr != nil {
			log.Printf("task: skip group %s: invalid JSON", entry.Name())
			continue
		}
		if validateErr := g.Validate(); validateErr != nil {
			log.Printf("task: skip group %s: %v", entry.Name(), validateErr)
			continue
		}
		if _, exists := loaded[g.ID]; exists {
			log.Printf("task: skip group %s: duplicate ID %s", entry.Name(), g.ID)
			continue
		}
		loaded[g.ID] = &g
	}
	s.mu.Lock()
	s.groups = loaded
	s.mu.Unlock()
	return s.recover()
}

// recover marks groups a crash left running as interrupted and persists the
// change. Groups awaiting confirmation stay confirmable.
func (s *Store) recover() error {
	now := s.now()
	s.mu.Lock()
	var changed []Group
	for _, g := range s.groups {
		before := g.Status
		g.MarkInterrupted(now)
		if g.Status != before {
			changed = append(changed, g.Clone())
		}
	}
	cp := make([]Group, len(changed))
	copy(cp, changed)
	s.mu.Unlock()
	for i := range cp {
		if err := s.Save(&cp[i]); err != nil {
			return err
		}
	}
	return nil
}

// List returns the summaries ordered by recent update, newest first.
func (s *Store) List() []Summary {
	s.mu.Lock()
	out := make([]Summary, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, Summary{
			ID:             g.ID,
			ConversationID: g.ConversationID,
			MessageIndex:   g.MessageIndex,
			Status:         g.Status,
			Tasks:          len(g.Tasks),
			UpdatedAt:      g.UpdatedAt,
		})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns a copy of one group.
func (s *Store) Get(id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok {
		return Group{}, false
	}
	return g.Clone(), true
}

// Save adds or replaces one group and writes it atomically.
func (s *Store) Save(g *Group) error {
	if g == nil {
		return errors.New("task: nil group")
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("task: invalid group: %w", err)
	}
	if !ValidGroupID(g.ID) {
		return fmt.Errorf("task: invalid group ID %q", g.ID)
	}
	copy := g.Clone()
	s.mu.Lock()
	s.groups[g.ID] = &copy
	s.mu.Unlock()
	if s.dir == "" {
		return nil
	}
	return writeGroup(s.dir, &copy)
}

// Delete removes one group and its file.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	_, ok := s.groups[id]
	delete(s.groups, id)
	s.mu.Unlock()
	if !ok {
		return ErrGroupNotFound
	}
	if s.dir == "" {
		return nil
	}
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("task: delete group: %w", err)
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+taskExt)
}

func writeGroup(dir string, g *Group) error {
	data, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("task: encode group: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		return fmt.Errorf("task: create task directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "tmp-*.json")
	if err != nil {
		return fmt.Errorf("task: create group file: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(storeFileMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("task: set group permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("task: write group: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("task: sync group: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("task: close group: %w", err)
	}
	if err := os.Rename(temp, filepath.Join(dir, g.ID+taskExt)); err != nil {
		return fmt.Errorf("task: replace group: %w", err)
	}
	temp = ""
	return nil
}
