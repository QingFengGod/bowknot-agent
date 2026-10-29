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
	AgentID   string
}

// Store keeps the conversations of every agent, one JSON file each under
// agents/<id>/sessions/. Every method is safe to call from any goroutine; the
// view only reads snapshots.
type Store struct {
	mu    sync.Mutex
	root  string
	now   func() time.Time
	convs map[string]*Conversation
}

// NewStore returns a store over the agents root. An empty root keeps
// conversations in memory only, which the tests use; Load then does nothing.
func NewStore(root string) *Store {
	return &Store{
		root:  root,
		now:   time.Now,
		convs: map[string]*Conversation{},
	}
}

// Root returns the agents root the store loads and saves.
func (s *Store) Root() string { return s.root }

// normalizeAgentID maps the main agent IDs to the empty ID: both MainAgentID
// and the empty string mean the main agent.
func normalizeAgentID(id string) string {
	if id == MainAgentID {
		return ""
	}
	return id
}

// SessionsDir returns the directory of one agent's conversations, or "" when
// the store runs in memory.
func (s *Store) SessionsDir(agentID string) string {
	if s.root == "" {
		return ""
	}
	id := normalizeAgentID(agentID)
	if id == "" {
		id = MainAgentID
	}
	return NewWorkspace(s.root, id).SessionsDir()
}

// Load reads every valid conversation of every agent workspace. Malformed,
// oversized and unsupported files are logged and skipped, never deleted. A
// conversation whose stored agent does not match its directory is skipped, so
// the two agent lists stay isolated.
func (s *Store) Load() error {
	if s.root == "" {
		return nil
	}
	if err := os.MkdirAll(s.SessionsDir(""), storeDirMode); err != nil {
		return fmt.Errorf("agent: create conversation directory: %w", err)
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("agent: list agent workspaces: %w", err)
	}
	loaded := map[string]*Conversation{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID, ok := sessionAgentID(entry.Name())
		if !ok {
			continue
		}
		s.loadAgentSessions(NewWorkspace(s.root, entry.Name()).SessionsDir(), agentID, loaded)
	}
	s.mu.Lock()
	s.convs = loaded
	s.mu.Unlock()
	return nil
}

// sessionAgentID maps one workspace directory name to the agent ID the store
// uses. Directories that are not an agent workspace are skipped.
func sessionAgentID(dirName string) (string, bool) {
	switch {
	case dirName == MainAgentID:
		return "", true
	case ValidSubAgentID(dirName):
		return dirName, true
	default:
		return "", false
	}
}

func (s *Store) loadAgentSessions(dir, agentID string, loaded map[string]*Conversation) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Printf("agent: list conversations in %s: %v", dir, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), conversationExt) {
			continue
		}
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
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			log.Printf("agent: skip conversation %s: %v", entry.Name(), err)
			continue
		}
		var c Conversation
		if err := json.Unmarshal(data, &c); err != nil {
			log.Printf("agent: skip conversation %s: invalid JSON", entry.Name())
			continue
		}
		c.AgentID = normalizeAgentID(c.AgentID)
		if err := c.Validate(); err != nil {
			log.Printf("agent: skip conversation %s: %v", entry.Name(), err)
			continue
		}
		if c.AgentID != agentID {
			log.Printf("agent: skip conversation %s: agent %q does not match directory", entry.Name(), c.AgentID)
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
}

// List returns the summaries of the main agent ordered by recent update.
func (s *Store) List() []Summary {
	return s.ListByAgent("")
}

// ListByAgent returns the summaries of one agent ordered by recent update. An
// empty agent ID lists the conversations of the main agent.
func (s *Store) ListByAgent(agentID string) []Summary {
	agentID = normalizeAgentID(agentID)
	s.mu.Lock()
	out := make([]Summary, 0, len(s.convs))
	for _, c := range s.convs {
		if c.AgentID != agentID {
			continue
		}
		out = append(out, Summary{
			ID:        c.ID,
			Title:     c.TitleText(),
			UpdatedAt: c.UpdatedAt,
			Messages:  len(c.Messages),
			Model:     c.Model,
			AgentID:   c.AgentID,
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

// Create adds an empty conversation of the main agent and returns a copy.
func (s *Store) Create() Conversation {
	return s.CreateFor("")
}

// CreateFor adds an empty conversation of one agent and returns a copy. The
// empty ID and "main" both create a main agent conversation.
func (s *Store) CreateFor(agentID string) Conversation {
	agentID = normalizeAgentID(agentID)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		c := NewConversation(now)
		c.AgentID = agentID
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

// Save writes one conversation atomically into its agent's sessions
// directory: a temporary file in the same directory replaces the old one.
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
	if s.root == "" {
		return nil
	}
	if !ValidConversationID(id) {
		return fmt.Errorf("agent: invalid conversation ID %q", id)
	}
	agentID := normalizeAgentID(copy.AgentID)
	if agentID != "" && !ValidSubAgentID(agentID) {
		return fmt.Errorf("agent: invalid agent ID %q", copy.AgentID)
	}
	data, err := json.Marshal(copy)
	if err != nil {
		return fmt.Errorf("agent: encode conversation: %w", err)
	}
	data = append(data, '\n')
	dir := s.SessionsDir(agentID)
	if err := os.MkdirAll(dir, storeDirMode); err != nil {
		return fmt.Errorf("agent: create conversation directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "tmp-*.json")
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
	if err := os.Rename(temp, s.path(agentID, id)); err != nil {
		return fmt.Errorf("agent: replace conversation: %w", err)
	}
	temp = ""
	return nil
}

// DeleteAgent removes every conversation of one sub agent, both in memory and
// on disk. The main agent sessions are removed one by one instead.
func (s *Store) DeleteAgent(agentID string) error {
	agentID = normalizeAgentID(agentID)
	if agentID == "" {
		return errors.New("主 Agent 会话不可批量删除")
	}
	if !ValidSubAgentID(agentID) {
		return fmt.Errorf("agent: invalid agent ID %q", agentID)
	}
	s.mu.Lock()
	for id, c := range s.convs {
		if normalizeAgentID(c.AgentID) == agentID {
			delete(s.convs, id)
		}
	}
	s.mu.Unlock()
	if s.root == "" {
		return nil
	}
	if err := os.RemoveAll(s.SessionsDir(agentID)); err != nil {
		return fmt.Errorf("agent: delete agent sessions: %w", err)
	}
	return nil
}

// Delete removes one conversation file and its in-memory copy.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	conv, ok := s.convs[id]
	agentID := ""
	if ok {
		agentID = normalizeAgentID(conv.AgentID)
	}
	delete(s.convs, id)
	s.mu.Unlock()
	if !ok {
		return ErrConversationNotFound
	}
	if s.root == "" {
		return nil
	}
	if !ValidConversationID(id) {
		return fmt.Errorf("agent: invalid conversation ID %q", id)
	}
	if err := os.Remove(s.path(agentID, id)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("agent: delete conversation: %w", err)
	}
	return nil
}

func (s *Store) path(agentID, id string) string {
	return filepath.Join(s.SessionsDir(agentID), id+conversationExt)
}
