package agent

import (
	"crypto/rand"
	"encoding/hex"
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

// 子 Agent 配置的结构与上限。schema 变更时必须递增并处理旧版本。
const (
	// SubAgentSchemaVersion is the sub agent file schema this build reads.
	SubAgentSchemaVersion = 1
	// SubAgentsFileName is the sub agent file inside the agent directory.
	SubAgentsFileName = "subagents.json"
	// MaxSubAgentNameRunes caps the display name.
	MaxSubAgentNameRunes = 32
	// MaxPersonaRunes caps the persona text.
	MaxPersonaRunes = 2000
	// MaxSubAgentFileBytes caps the sub agent file size.
	MaxSubAgentFileBytes = 2 << 20
)

// Sub agent store errors.
var (
	ErrSubAgentNotFound  = errors.New("子 Agent 不存在")
	ErrSubAgentLimit     = fmt.Errorf("子 Agent 已达上限 %d 个", MaxSubAgents)
	ErrSubAgentNameTaken = errors.New("子 Agent 名称已被使用")
)

// SubAgent is one user-defined helper agent. It binds one chat route target
// and supplies the system persona of its calls.
type SubAgent struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Persona    string `json:"persona"`
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// PersonaLine returns the first line of the persona for list rows.
func (s SubAgent) PersonaLine() string {
	line := strings.TrimSpace(s.Persona)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// ValidSubAgentID reports whether id has the form a-xxxxxxxx.
func ValidSubAgentID(id string) bool {
	if len(id) != 10 || !strings.HasPrefix(id, "a-") {
		return false
	}
	for _, r := range id[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func newSubAgentID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("a-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "a-" + hex.EncodeToString(buf[:])
}

// Normalize trims the editable fields.
func (s SubAgent) Normalize() SubAgent {
	s.Name = strings.TrimSpace(s.Name)
	s.Persona = strings.TrimSpace(s.Persona)
	s.ProviderID = strings.TrimSpace(s.ProviderID)
	s.Model = strings.TrimSpace(s.Model)
	return s
}

// Validate checks the fields of a sub agent.
func (s SubAgent) Validate() error {
	if !ValidSubAgentID(s.ID) {
		return errors.New("子 Agent ID 非法")
	}
	if err := checkRunes("名称", s.Name, 1, MaxSubAgentNameRunes); err != nil {
		return err
	}
	if err := checkRunes("人设", s.Persona, 1, MaxPersonaRunes); err != nil {
		return err
	}
	if s.ProviderID == "" || s.Model == "" {
		return errors.New("请选择子 Agent 的模型")
	}
	if s.CreatedAt <= 0 || s.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	return nil
}

func checkRunes(name, value string, min, max int) error {
	n := len([]rune(strings.TrimSpace(value)))
	if n < min {
		return fmt.Errorf("%s为空", name)
	}
	if n > max {
		return fmt.Errorf("%s %d 字符，超过上限 %d", name, n, max)
	}
	return nil
}

// subAgentFile is the on-disk shape of the sub agent list.
type subAgentFile struct {
	Schema int        `json:"schema"`
	Agents []SubAgent `json:"agents"`
}

// SubAgentStore keeps the sub agent definitions in one JSON file. Every
// method is safe to call from any goroutine.
type SubAgentStore struct {
	mu     sync.Mutex
	path   string
	now    func() time.Time
	agents []SubAgent
	// corrupt reports that the file exists but could not be read, so the page
	// can warn instead of showing an empty list as if nothing was defined.
	corrupt bool
}

// NewSubAgentStore returns a store over path. An empty path keeps the list in
// memory, which the tests use.
func NewSubAgentStore(path string) *SubAgentStore {
	return &SubAgentStore{path: path, now: time.Now}
}

// Path returns the file the store reads and writes.
func (s *SubAgentStore) Path() string { return s.path }

// Corrupt reports whether loading skipped an unreadable file. The original
// file is kept.
func (s *SubAgentStore) Corrupt() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrupt
}

// Load reads the file. A missing file is an empty list; a malformed,
// unsupported or oversized file leaves an empty list, keeps the file and
// records corrupt.
func (s *SubAgentStore) Load() error {
	if s.path == "" {
		return nil
	}
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent: stat sub agents: %w", err)
	}
	if info.Size() > MaxSubAgentFileBytes {
		s.setCorrupt()
		log.Printf("agent: skip sub agents: file exceeds %d bytes", MaxSubAgentFileBytes)
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return fmt.Errorf("agent: read sub agents: %w", err)
	}
	var file subAgentFile
	if err := json.Unmarshal(data, &file); err != nil {
		s.setCorrupt()
		log.Printf("agent: skip sub agents: invalid JSON")
		return nil
	}
	if file.Schema != SubAgentSchemaVersion {
		s.setCorrupt()
		log.Printf("agent: skip sub agents: schema %d is not supported", file.Schema)
		return nil
	}
	if len(file.Agents) > MaxSubAgents {
		s.setCorrupt()
		log.Printf("agent: skip sub agents: %d entries exceed %d", len(file.Agents), MaxSubAgents)
		return nil
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	agents := make([]SubAgent, 0, len(file.Agents))
	for i, a := range file.Agents {
		a = a.Normalize()
		if err := a.Validate(); err != nil {
			s.setCorrupt()
			log.Printf("agent: skip sub agents: entry %d is invalid: %v", i+1, err)
			return nil
		}
		if seen[a.ID] || names[strings.ToLower(a.Name)] {
			s.setCorrupt()
			log.Printf("agent: skip sub agents: entry %d repeats an ID or name", i+1)
			return nil
		}
		seen[a.ID] = true
		names[strings.ToLower(a.Name)] = true
		agents = append(agents, a)
	}
	s.mu.Lock()
	s.agents = agents
	s.corrupt = false
	s.mu.Unlock()
	return nil
}

func (s *SubAgentStore) setCorrupt() {
	s.mu.Lock()
	s.agents = nil
	s.corrupt = true
	s.mu.Unlock()
}

// List returns a copy of the sub agents in creation order.
func (s *SubAgentStore) List() []SubAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SubAgent(nil), s.agents...)
}

// Get returns a copy of one sub agent.
func (s *SubAgentStore) Get(id string) (SubAgent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.agents {
		if a.ID == id {
			return a, true
		}
	}
	return SubAgent{}, false
}

// Count returns how many sub agents are defined.
func (s *SubAgentStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents)
}

// Save adds or updates a sub agent. An empty ID creates one, and an empty
// name or persona is rejected. The name is unique regardless of case.
func (s *SubAgentStore) Save(in SubAgent) (SubAgent, error) {
	in = in.Normalize()
	if in.ID != "" && !ValidSubAgentID(in.ID) {
		return SubAgent{}, errors.New("子 Agent ID 非法")
	}
	if err := checkRunes("名称", in.Name, 1, MaxSubAgentNameRunes); err != nil {
		return SubAgent{}, err
	}
	if err := checkRunes("人设", in.Persona, 1, MaxPersonaRunes); err != nil {
		return SubAgent{}, err
	}
	if in.ProviderID == "" || in.Model == "" {
		return SubAgent{}, errors.New("请选择子 Agent 的模型")
	}

	s.mu.Lock()
	now := s.now()
	next := append([]SubAgent(nil), s.agents...)
	index := -1
	for i := range next {
		if in.ID != "" && next[i].ID == in.ID {
			index = i
		}
		if strings.EqualFold(next[i].Name, in.Name) && (in.ID == "" || next[i].ID != in.ID) {
			s.mu.Unlock()
			return SubAgent{}, ErrSubAgentNameTaken
		}
	}
	if index < 0 {
		if in.ID == "" {
			in.ID = newSubAgentID()
			for s.idExistsLocked(next, in.ID) {
				in.ID = newSubAgentID()
			}
		}
		if len(next) >= MaxSubAgents {
			s.mu.Unlock()
			return SubAgent{}, ErrSubAgentLimit
		}
		in.CreatedAt = now.Unix()
		in.UpdatedAt = now.Unix()
		next = append(next, in)
	} else {
		prev := next[index]
		in.CreatedAt = prev.CreatedAt
		in.UpdatedAt = now.Unix()
		next[index] = in
	}
	s.agents = next
	s.mu.Unlock()

	if err := s.persist(next); err != nil {
		return in, err
	}
	return in, nil
}

func (s *SubAgentStore) idExistsLocked(agents []SubAgent, id string) bool {
	for _, a := range agents {
		if a.ID == id {
			return true
		}
	}
	return false
}

// SetEnabled turns dispatch of one sub agent on or off.
func (s *SubAgentStore) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	next := append([]SubAgent(nil), s.agents...)
	index := -1
	for i := range next {
		if next[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return ErrSubAgentNotFound
	}
	next[index].Enabled = enabled
	next[index].UpdatedAt = s.now().Unix()
	s.agents = next
	s.mu.Unlock()
	return s.persist(next)
}

// Delete removes one sub agent. Historical tasks keep their name snapshot.
func (s *SubAgentStore) Delete(id string) error {
	s.mu.Lock()
	next := make([]SubAgent, 0, len(s.agents))
	found := false
	for _, a := range s.agents {
		if a.ID == id {
			found = true
			continue
		}
		next = append(next, a)
	}
	if !found {
		s.mu.Unlock()
		return ErrSubAgentNotFound
	}
	s.agents = next
	s.mu.Unlock()
	return s.persist(next)
}

// persist writes the list atomically; a failure keeps the in-memory change.
func (s *SubAgentStore) persist(agents []SubAgent) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), storeDirMode); err != nil {
		return fmt.Errorf("agent: create agent directory: %w", err)
	}
	data, err := json.Marshal(subAgentFile{Schema: SubAgentSchemaVersion, Agents: agents})
	if err != nil {
		return fmt.Errorf("agent: encode sub agents: %w", err)
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(s.path), "tmp-subagents-*.json")
	if err != nil {
		return fmt.Errorf("agent: create sub agent file: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(storeFileMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: set sub agent permissions: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: write sub agents: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("agent: sync sub agents: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("agent: close sub agents: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		return fmt.Errorf("agent: replace sub agents: %w", err)
	}
	temp = ""
	return nil
}
