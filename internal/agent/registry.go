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
	"sort"
	"strings"
	"sync"
	"time"
)

// Agent metadata layout and limits. The schema must be bumped when the agent
// file changes, and old versions must be handled.
const (
	// AgentSchemaVersion is the agent metadata schema this build reads.
	AgentSchemaVersion = 1
	// AgentMetaFileName is the metadata file inside each agent workspace.
	AgentMetaFileName = "agent.json"
	// MaxSubAgents caps how many sub agents a user may define. The main agent
	// does not count against it.
	MaxSubAgents = 20
	// MaxAgentNameRunes caps the display name.
	MaxAgentNameRunes = 32
	// MaxAgentMetaBytes caps one agent metadata file.
	MaxAgentMetaBytes = 64 << 10
	// DefaultMainAgentName is the name of the main agent until the user edits
	// it.
	DefaultMainAgentName = "主 Agent"
)

// Registry errors.
var (
	ErrAgentNotFound     = errors.New("Agent 不存在")
	ErrAgentCorrupt      = errors.New("Agent 元数据损坏")
	ErrAgentLimit        = fmt.Errorf("子 Agent 已达上限 %d 个", MaxSubAgents)
	ErrAgentNameTaken    = errors.New("Agent 名称已被使用")
	ErrMainAgentDelete   = errors.New("主 Agent 不可删除")
	ErrMainAgentDisabled = errors.New("主 Agent 不能停用")
)

// Agent is one configurable agent. Its persona lives in PERSONA.md, its user
// notes in USER.md and its plugin list in plugins.json inside the workspace.
type Agent struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar,omitempty"`
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// IsMain reports whether the agent is the fixed main agent.
func (a Agent) IsMain() bool { return a.ID == MainAgentID }

// Normalize trims the editable text fields.
func (a Agent) Normalize() Agent {
	a.ID = strings.TrimSpace(a.ID)
	a.Name = strings.TrimSpace(a.Name)
	a.Avatar = strings.TrimSpace(a.Avatar)
	a.ProviderID = strings.TrimSpace(a.ProviderID)
	a.Model = strings.TrimSpace(a.Model)
	return a
}

// Validate checks one agent read from disk.
func (a Agent) Validate() error {
	if !ValidAgentID(a.ID) {
		return errors.New("Agent ID 非法")
	}
	if err := checkAgentName(a.Name); err != nil {
		return err
	}
	if a.CreatedAt <= 0 || a.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	return nil
}

// ValidAgentID reports whether id is the main agent or a sub agent ID.
func ValidAgentID(id string) bool {
	return id == MainAgentID || ValidSubAgentID(id)
}

func newAgentID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("a-%08x", time.Now().UnixNano()&0xffffffff)
	}
	return "a-" + hex.EncodeToString(buf[:])
}

func checkAgentName(name string) error {
	n := len([]rune(strings.TrimSpace(name)))
	if n == 0 {
		return errors.New("名称为空")
	}
	if n > MaxAgentNameRunes {
		return fmt.Errorf("名称 %d 字符，超过上限 %d", n, MaxAgentNameRunes)
	}
	return nil
}

// agentFile is the on-disk shape of one agent metadata file.
type agentFile struct {
	Schema int   `json:"schema"`
	Agent  Agent `json:"agent"`
}

// Registry keeps the main agent and every sub agent under the agents root, one
// workspace directory each. Every method is safe to call from any goroutine.
type Registry struct {
	root string
	now  func() time.Time

	mu     sync.Mutex
	agents map[string]Agent
}

// NewRegistry returns a registry over root. An empty root keeps the agents in
// memory only, which the tests use.
func NewRegistry(root string) *Registry {
	return &Registry{
		root:   root,
		now:    time.Now,
		agents: map[string]Agent{},
	}
}

// Dir returns the agents root.
func (r *Registry) Dir() string { return r.root }

// Load scans the agents root. A missing main workspace is rebuilt from the
// default template; a corrupt sub agent directory is logged and skipped, never
// deleted. Legacy agent/ and subagents.json data is not read.
func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.root == "" {
		if _, ok := r.agents[MainAgentID]; !ok {
			r.agents[MainAgentID] = r.newMainAgent()
		}
		return nil
	}
	if err := os.MkdirAll(r.root, storeDirMode); err != nil {
		return fmt.Errorf("agent: create agents directory: %w", err)
	}

	main, err := r.loadMainLocked()
	if err != nil {
		return err
	}
	agents := map[string]Agent{MainAgentID: main}
	names := map[string]bool{strings.ToLower(main.Name): true}

	entries, err := os.ReadDir(r.root)
	if err != nil {
		return fmt.Errorf("agent: list agent workspaces: %w", err)
	}
	subs := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == MainAgentID {
			continue
		}
		// Files such as a legacy subagents.json are ignored.
		if !ValidSubAgentID(entry.Name()) {
			log.Printf("agent: skip agent directory %s: invalid ID", entry.Name())
			continue
		}
		if subs >= MaxSubAgents {
			log.Printf("agent: skip agent directory %s: sub agent limit reached", entry.Name())
			continue
		}
		a, err := readAgentMeta(filepath.Join(r.root, entry.Name(), AgentMetaFileName))
		if err != nil {
			log.Printf("agent: skip agent %s: %v", entry.Name(), err)
			continue
		}
		a = a.Normalize()
		if a.ID != entry.Name() || a.IsMain() {
			log.Printf("agent: skip agent %s: metadata ID %q does not match", entry.Name(), a.ID)
			continue
		}
		if err := a.Validate(); err != nil {
			log.Printf("agent: skip agent %s: %v", entry.Name(), err)
			continue
		}
		if names[strings.ToLower(a.Name)] {
			log.Printf("agent: skip agent %s: duplicate name %q", entry.Name(), a.Name)
			continue
		}
		ws := NewWorkspace(r.root, a.ID)
		if err := ws.Ensure(); err != nil {
			log.Printf("agent: skip agent %s: %v", entry.Name(), err)
			continue
		}
		if _, err := ws.Load(); err != nil {
			// A corrupt text file is reported but the agent stays usable.
			log.Printf("agent: agent %s: %v", entry.Name(), err)
		}
		names[strings.ToLower(a.Name)] = true
		agents[a.ID] = a
		subs++
	}
	r.agents = agents
	return nil
}

// loadMainLocked ensures agents/main exists and returns its metadata. The
// caller holds the lock.
func (r *Registry) loadMainLocked() (Agent, error) {
	ws := NewWorkspace(r.root, MainAgentID)
	a, err := readAgentMeta(ws.Path(AgentMetaFileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		a = r.newMainAgent()
	case err != nil:
		log.Printf("agent: rebuild main agent metadata: %v", err)
		a = r.newMainAgent()
	default:
		a = a.Normalize()
		if err := a.Validate(); err != nil || a.ID != MainAgentID {
			log.Printf("agent: rebuild main agent metadata: invalid agent")
			a = r.newMainAgent()
		}
	}
	a.ID = MainAgentID
	// The main agent is always enabled.
	a.Enabled = true
	if err := ws.Ensure(); err != nil {
		return Agent{}, fmt.Errorf("agent: ensure main workspace: %w", err)
	}
	if err := r.persistLocked(a); err != nil {
		return Agent{}, err
	}
	if _, err := ws.Load(); err != nil {
		log.Printf("agent: main workspace: %v", err)
	}
	return a, nil
}

func (r *Registry) newMainAgent() Agent {
	now := r.now().Unix()
	return Agent{
		ID:        MainAgentID,
		Name:      DefaultMainAgentName,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// List returns the main agent first, then the sub agents by creation time.
func (r *Registry) List() []Agent {
	r.mu.Lock()
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsMain() != out[j].IsMain() {
			return out[i].IsMain()
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Main returns the main agent. It is zero before Load.
func (r *Registry) Main() Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agents[MainAgentID]
}

// Get returns one agent.
func (r *Registry) Get(id string) (Agent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[id]
	return a, ok
}

// Count returns how many sub agents are defined.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.subCountLocked()
}

func (r *Registry) subCountLocked() int {
	n := len(r.agents)
	if _, ok := r.agents[MainAgentID]; ok {
		n--
	}
	return n
}

// Create adds a sub agent with a generated ID. The name is unique regardless
// of case and the sub agent limit is enforced.
func (r *Registry) Create(in Agent) (Agent, error) {
	in = in.Normalize()
	if in.ID != "" {
		return Agent{}, errors.New("创建 Agent 时不能指定 ID")
	}
	if err := checkAgentName(in.Name); err != nil {
		return Agent{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nameTakenLocked(in.Name, "") {
		return Agent{}, ErrAgentNameTaken
	}
	if r.subCountLocked() >= MaxSubAgents {
		return Agent{}, ErrAgentLimit
	}
	id := newAgentID()
	for {
		if _, exists := r.agents[id]; !exists {
			break
		}
		id = newAgentID()
	}
	now := r.now().Unix()
	in.ID = id
	in.CreatedAt = now
	in.UpdatedAt = now
	if err := r.persistLocked(in); err != nil {
		return in, err
	}
	r.agents[id] = in
	return in, nil
}

// Update replaces the editable fields of an existing agent. The ID is
// immutable and the name stays unique regardless of case. The main agent
// stays enabled.
func (r *Registry) Update(in Agent) (Agent, error) {
	in = in.Normalize()
	if !ValidAgentID(in.ID) {
		return Agent{}, errors.New("Agent ID 非法")
	}
	if err := checkAgentName(in.Name); err != nil {
		return Agent{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.agents[in.ID]
	if !ok {
		return Agent{}, ErrAgentNotFound
	}
	if r.nameTakenLocked(in.Name, in.ID) {
		return Agent{}, ErrAgentNameTaken
	}
	in.CreatedAt = prev.CreatedAt
	in.UpdatedAt = r.now().Unix()
	if in.IsMain() {
		in.Enabled = true
	}
	if err := r.persistLocked(in); err != nil {
		return in, err
	}
	r.agents[in.ID] = in
	return in, nil
}

// SetEnabled turns task dispatch for one sub agent on or off. The main agent
// is always enabled.
func (r *Registry) SetEnabled(id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == MainAgentID {
		return ErrMainAgentDisabled
	}
	a, ok := r.agents[id]
	if !ok {
		return ErrAgentNotFound
	}
	a.Enabled = enabled
	a.UpdatedAt = r.now().Unix()
	if err := r.persistLocked(a); err != nil {
		return err
	}
	r.agents[id] = a
	return nil
}

// Delete removes one sub agent and its whole workspace. The main agent cannot
// be deleted.
func (r *Registry) Delete(id string) error {
	if id == MainAgentID {
		return ErrMainAgentDelete
	}
	if !ValidSubAgentID(id) {
		return ErrAgentNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[id]; !ok {
		return ErrAgentNotFound
	}
	delete(r.agents, id)
	if r.root == "" {
		return nil
	}
	if err := os.RemoveAll(NewWorkspace(r.root, id).Dir()); err != nil {
		return fmt.Errorf("agent: delete agent workspace: %w", err)
	}
	return nil
}

func (r *Registry) nameTakenLocked(name, excludeID string) bool {
	for id, a := range r.agents {
		if id != excludeID && strings.EqualFold(a.Name, name) {
			return true
		}
	}
	return false
}

// persistLocked writes one metadata file atomically and creates the workspace
// directories and default files. The caller holds the lock.
func (r *Registry) persistLocked(a Agent) error {
	if r.root == "" {
		return nil
	}
	ws := NewWorkspace(r.root, a.ID)
	if err := ws.Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(agentFile{Schema: AgentSchemaVersion, Agent: a})
	if err != nil {
		return fmt.Errorf("agent: encode agent metadata: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(ws.Path(AgentMetaFileName), data, storeFileMode); err != nil {
		return fmt.Errorf("agent: save agent metadata: %w", err)
	}
	return nil
}

func readAgentMeta(path string) (Agent, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Agent{}, err
	}
	if info.Size() > MaxAgentMetaBytes {
		return Agent{}, fmt.Errorf("metadata exceeds %d bytes", MaxAgentMetaBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, err
	}
	var file agentFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Agent{}, invalidAgentFile("invalid JSON")
	}
	if file.Schema != AgentSchemaVersion {
		return Agent{}, invalidAgentFile(fmt.Sprintf("schema %d is not supported", file.Schema))
	}
	return file.Agent, nil
}

func invalidAgentFile(detail string) error {
	return fmt.Errorf("%w: %s", ErrAgentCorrupt, detail)
}
