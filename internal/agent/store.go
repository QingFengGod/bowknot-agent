package agent

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

// Store errors.
var (
	ErrConversationFull     = errors.New("会话消息已达上限")
	ErrConversationNotFound = errors.New("会话不存在")
	ErrNoAssistantReply     = errors.New("没有可重新生成的回复")
)

const (
	conversationExt = ".json"
	storeDirMode    = 0o700
	storeFileMode   = 0o600
)

// Summary is one row of the conversation list.
type Summary struct {
	ID        string
	Title     string
	UpdatedAt int64
	Messages  int
	Model     string
}

// Store keeps the conversations of the assistant, one JSON file each. Every
// method is safe to call from any goroutine; the view only reads snapshots.
type Store struct {
	mu    sync.Mutex
	dir   string
	now   func() time.Time
	convs map[string]*Conversation
}

// NewStore returns a store over dir. An empty dir keeps conversations in
// memory only, which the tests use; Load then does nothing.
func NewStore(dir string) *Store {
	return &Store{
		dir:   dir,
		now:   time.Now,
		convs: map[string]*Conversation{},
	}
}

// Dir returns the directory the store loads and saves.
func (s *Store) Dir() string { return s.dir }

// Load reads every valid conversation file. Malformed, oversized and
// unsupported files are logged and skipped, never deleted.
func (s *Store) Load() error {
	if s.dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.dir, storeDirMode); err != nil {
		return fmt.Errorf("agent: create conversation directory: %w", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("agent: list conversations: %w", err)
	}
	loaded := make(map[string]*Conversation, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), conversationExt) {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			log.Printf("agent: skip conversation %s: %v", entry.Name(), err)
			continue
		}
		if info.Size() > MaxConversationBytes {
			log.Printf("agent: skip conversation %s: file exceeds %d bytes",
				entry.Name(), MaxConversationBytes)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("agent: skip conversation %s: %v", entry.Name(), err)
			continue
		}
		var c Conversation
		if err := json.Unmarshal(data, &c); err != nil {
			log.Printf("agent: skip conversation %s: invalid JSON", entry.Name())
			continue
		}
		if err := c.Validate(); err != nil {
			log.Printf("agent: skip conversation %s: %v", entry.Name(), err)
			continue
		}
		if _, exists := loaded[c.ID]; exists {
			log.Printf("agent: skip conversation %s: duplicate ID %s", entry.Name(), c.ID)
			continue
		}
		if c.Title == "" {
			c.Title = DefaultTitle
		}
		cp := c
		loaded[c.ID] = &cp
	}
	s.mu.Lock()
	s.convs = loaded
	s.mu.Unlock()
	return nil
}

// List returns the summaries ordered by recent update, newest first.
func (s *Store) List() []Summary {
	s.mu.Lock()
	out := make([]Summary, 0, len(s.convs))
	for _, c := range s.convs {
		out = append(out, Summary{
			ID:        c.ID,
			Title:     c.TitleText(),
			UpdatedAt: c.UpdatedAt,
			Messages:  len(c.Messages),
			Model:     c.Model,
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

// Get returns a copy of one conversation.
func (s *Store) Get(id string) (Conversation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return Conversation{}, false
	}
	return c.Clone(), true
}

// Create adds an empty conversation and returns a copy.
func (s *Store) Create() Conversation {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		c := NewConversation(now)
		if _, exists := s.convs[c.ID]; !exists {
			s.convs[c.ID] = c
			return c.Clone()
		}
	}
}

// Append adds one message and returns its index. The title follows the first
// user message, and the conversation time moves to the message time.
func (s *Store) Append(id string, msg Message) (int, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return 0, ErrConversationNotFound
	}
	if len(c.Messages) >= MaxMessages {
		return 0, ErrConversationFull
	}
	if msg.At == 0 {
		msg.At = now.Unix()
	}
	if msg.Role == RoleUser && !c.TitleEdited && (c.Title == "" || c.Title == DefaultTitle) {
		c.Title = TitleFrom(msg.Content)
	}
	if msg.Model != "" {
		c.Model = msg.Model
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = msg.At
	return len(c.Messages) - 1, nil
}

// Rename sets a conversation title. Whitespace is collapsed, the result is
// cut to the title limit and an empty title falls back to the default. A
// renamed conversation keeps its title when its first user message arrives.
// The update time does not change, so the list order stays steady.
func (s *Store) Rename(id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return ErrConversationNotFound
	}
	c.Title = TitleFrom(title)
	c.TitleEdited = true
	return nil
}

// SetContent replaces the content of a message while it streams. It does not
// change the conversation time, so the list order stays steady.
func (s *Store) SetContent(id string, index int, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return ErrConversationNotFound
	}
	if index < 0 || index >= len(c.Messages) {
		return ErrConversationNotFound
	}
	c.Messages[index].Content = content
	return nil
}

// FinishMessage replaces a message with its final state and moves the
// conversation time.
func (s *Store) FinishMessage(id string, index int, msg Message) error {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return ErrConversationNotFound
	}
	if index < 0 || index >= len(c.Messages) {
		return ErrConversationNotFound
	}
	if msg.At == 0 {
		msg.At = now.Unix()
	}
	c.Messages[index] = msg
	c.UpdatedAt = msg.At
	if msg.Model != "" {
		c.Model = msg.Model
	}
	return nil
}

// DropLastAssistant removes a trailing assistant reply for a regeneration.
// It reports false when the last message is not an assistant reply.
func (s *Store) DropLastAssistant(id string) (bool, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return false, ErrConversationNotFound
	}
	if len(c.Messages) == 0 || c.Messages[len(c.Messages)-1].Role != RoleAssistant {
		return false, nil
	}
	c.Messages = c.Messages[:len(c.Messages)-1]
	c.UpdatedAt = now.Unix()
	return true, nil
}

// Save writes one conversation atomically: a temporary file in the same
// directory replaces the old one.
func (s *Store) Save(id string) error {
	s.mu.Lock()
	c, ok := s.convs[id]
	var copy Conversation
	if ok {
		copy = c.Clone()
	}
	s.mu.Unlock()
	if !ok {
		return ErrConversationNotFound
	}
	if s.dir == "" {
		return nil
	}
	if !ValidConversationID(id) {
		return fmt.Errorf("agent: invalid conversation ID %q", id)
	}
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("agent: encode conversation: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.dir, storeDirMode); err != nil {
		return fmt.Errorf("agent: create conversation directory: %w", err)
	}
	f, err := os.CreateTemp(s.dir, "tmp-*.json")
	if err != nil {
		return fmt.Errorf("agent: create conversation file: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(storeFileMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: set conversation permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: write conversation: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: sync conversation: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("agent: close conversation: %w", err)
	}
	if err := os.Rename(temp, s.path(id)); err != nil {
		return fmt.Errorf("agent: replace conversation: %w", err)
	}
	temp = ""
	return nil
}

// Delete removes one conversation file and its in-memory copy.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	_, ok := s.convs[id]
	delete(s.convs, id)
	s.mu.Unlock()
	if !ok {
		return ErrConversationNotFound
	}
	if s.dir == "" {
		return nil
	}
	if !ValidConversationID(id) {
		return fmt.Errorf("agent: invalid conversation ID %q", id)
	}
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("agent: delete conversation: %w", err)
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+conversationExt)
}
