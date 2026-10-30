package group

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
	groupFileName = "group.json"
	runExt        = ".json"
	storeDirMode  = 0o700
	storeFileMode = 0o600
)

// Store 错误。
var (
	// ErrGroupNotFound 表示群 ID 未知。
	ErrGroupNotFound = errors.New("群不存在")
	// ErrRunNotFound 表示 Run ID 未知。
	ErrRunNotFound = errors.New("Run 不存在")
	// ErrGroupLimit 表示群数量已达上限。
	ErrGroupLimit = fmt.Errorf("群数量已达上限 %d，请先删除不再使用的群", MaxGroups)
	// ErrRunLimit 表示该群的 Run 数量已达上限。
	ErrRunLimit = fmt.Errorf("该群的 Run 已达上限 %d", MaxRunsPerGroup)
)

// GroupSummary 是群列表的一行。
type GroupSummary struct {
	ID        string
	Name      string
	Members   int
	Runs      int
	UpdatedAt int64
}

// RunSummary 是 Run 列表的一行，供任务中心与群聊页使用。
type RunSummary struct {
	ID           string
	GroupID      string
	GroupName    string
	Goal         string
	Status       RunStatus
	Tasks        int
	Finished     int
	GroupDeleted bool
	UpdatedAt    int64
}

// Store 以 groupsDir/<id>/group.json 保存群定义，以 runsDir/<run>.json 保存
// Run。Run 与群目录分离，删除群时 Run 记录保留并标记群已删除（FR-GRP-10）。
// 每个方法都可从任意 goroutine 调用，界面只读取快照。
type Store struct {
	groupsDir string
	runsDir   string
	now       func() time.Time

	mu         sync.Mutex
	groups     map[string]*Group
	runs       map[string]*Run
	messages   map[string][]Message
	messageSeq map[string]int
}

// NewStore 返回一个存储。空目录只在内存中保存，测试使用。
func NewStore(groupsDir, runsDir string) *Store {
	return &Store{
		groupsDir:  groupsDir,
		runsDir:    runsDir,
		now:        time.Now,
		groups:     map[string]*Group{},
		runs:       map[string]*Run{},
		messages:   map[string][]Message{},
		messageSeq: map[string]int{},
	}
}

// GroupsDir 返回群定义目录。
func (s *Store) GroupsDir() string { return s.groupsDir }

// RunsDir 返回 Run 目录。
func (s *Store) RunsDir() string { return s.runsDir }

// Load 读取全部有效的群与 Run 文件，再把崩溃遗留的执行中 Run 标记为已中断。
// 损坏、超限与不支持的文件只记录并跳过，绝不删除。
func (s *Store) Load() error {
	groups, err := s.loadGroups()
	if err != nil {
		return err
	}
	runs, err := s.loadRuns(groups)
	if err != nil {
		return err
	}
	messages := s.loadMessages(runs)
	seqs := make(map[string]int, len(messages))
	for id, msgs := range messages {
		seqs[id] = messageSeqOf(msgs)
	}
	s.mu.Lock()
	s.groups = groups
	s.runs = runs
	s.messages = messages
	s.messageSeq = seqs
	s.mu.Unlock()
	return s.recover()
}

func (s *Store) loadGroups() (map[string]*Group, error) {
	groups := map[string]*Group{}
	if s.groupsDir == "" {
		return groups, nil
	}
	if err := os.MkdirAll(s.groupsDir, storeDirMode); err != nil {
		return nil, fmt.Errorf("group: 创建群目录: %w", err)
	}
	entries, err := os.ReadDir(s.groupsDir)
	if err != nil {
		return nil, fmt.Errorf("group: 读取群目录: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !ValidGroupID(entry.Name()) {
			continue
		}
		path := filepath.Join(s.groupsDir, entry.Name(), groupFileName)
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			// 群定义已删除但 Run 记录仍在，Run 加载时标记群已删除。
			continue
		}
		g, readErr := readGroupFile(path)
		if readErr != nil {
			log.Printf("group: 跳过群 %s: %v", entry.Name(), readErr)
			continue
		}
		if _, exists := groups[g.ID]; exists {
			log.Printf("group: 跳过群 %s: ID %s 重复", entry.Name(), g.ID)
			continue
		}
		groups[g.ID] = g
	}
	return groups, nil
}

func (s *Store) loadRuns(groups map[string]*Group) (map[string]*Run, error) {
	runs := map[string]*Run{}
	if s.runsDir == "" {
		return runs, nil
	}
	if err := os.MkdirAll(s.runsDir, storeDirMode); err != nil {
		return nil, fmt.Errorf("group: 创建 Run 目录: %w", err)
	}
	entries, err := os.ReadDir(s.runsDir)
	if err != nil {
		return nil, fmt.Errorf("group: 读取 Run 目录: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), runExt) {
			continue
		}
		r, readErr := readRunFile(filepath.Join(s.runsDir, entry.Name()))
		if readErr != nil {
			log.Printf("group: 跳过 Run %s: %v", entry.Name(), readErr)
			continue
		}
		if _, exists := runs[r.ID]; exists {
			log.Printf("group: 跳过 Run %s: ID %s 重复", entry.Name(), r.ID)
			continue
		}
		if r.GroupID != "" {
			g, ok := groups[r.GroupID]
			if !ok {
				r.GroupDeleted = true
			} else if r.GroupName == "" {
				r.GroupName = g.Name
			}
		}
		runs[r.ID] = r
	}
	return runs, nil
}

// recover 把崩溃遗留的执行中 Run 标记为已中断并持久化。待确认的 Run 保持
// 可确认状态。
func (s *Store) recover() error {
	now := s.now()
	s.mu.Lock()
	changed := make([]Run, 0)
	for _, r := range s.runs {
		if r.MarkInterrupted(now) {
			changed = append(changed, r.Clone())
		}
	}
	s.mu.Unlock()
	for i := range changed {
		if err := s.SaveRun(&changed[i]); err != nil {
			return err
		}
	}
	return nil
}

// Groups 返回按更新时间倒序的群列表。
func (s *Store) Groups() []GroupSummary {
	s.mu.Lock()
	out := make([]GroupSummary, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, GroupSummary{
			ID:        g.ID,
			Name:      g.Name,
			Members:   len(g.Members),
			Runs:      s.countRunsLocked(g.ID),
			UpdatedAt: g.UpdatedAt,
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

// GetGroup 返回群定义的副本。
func (s *Store) GetGroup(id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok {
		return Group{}, false
	}
	return g.Clone(), true
}

// SaveGroup 新增或替换一个群定义并原子写入。新群在达到 MaxGroups 时被拒绝，
// 已有群的更新总是接受。
func (s *Store) SaveGroup(g *Group) error {
	if g == nil {
		return errors.New("group: 群为空")
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("group: 群定义非法: %w", err)
	}
	copy := g.Clone()
	copy.Schema = SchemaVersion
	s.mu.Lock()
	if _, exists := s.groups[copy.ID]; !exists && len(s.groups) >= MaxGroups {
		s.mu.Unlock()
		return ErrGroupLimit
	}
	s.groups[copy.ID] = &copy
	s.mu.Unlock()
	if s.groupsDir == "" {
		return nil
	}
	return writeGroupFile(filepath.Join(s.groupsDir, copy.ID, groupFileName), &copy)
}

// DeleteGroup 删除群定义与群目录，取消运行中的 Run，相关 Run 记录保留并标记
// 「群聊已删除」（FR-GRP-10）。
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	_, ok := s.groups[id]
	delete(s.groups, id)
	now := s.now()
	var changed []Run
	for runID, r := range s.runs {
		if r.GroupID != id {
			continue
		}
		delete(s.messages, runID)
		delete(s.messageSeq, runID)
		before, deleted := r.Status, r.GroupDeleted
		if r.Status == RunInterrupted {
			r.Terminate(now)
		} else {
			r.Cancel(now)
		}
		r.GroupDeleted = true
		if r.Status != before || !deleted {
			changed = append(changed, r.Clone())
		}
	}
	s.mu.Unlock()
	if !ok {
		return ErrGroupNotFound
	}
	if s.groupsDir != "" {
		if err := os.RemoveAll(filepath.Join(s.groupsDir, id)); err != nil {
			return fmt.Errorf("group: 删除群目录: %w", err)
		}
	}
	for i := range changed {
		if err := s.SaveRun(&changed[i]); err != nil {
			return err
		}
	}
	return nil
}

// GroupCount 返回保存的群数量。
func (s *Store) GroupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.groups)
}

// Runs 返回指定群的 Run 摘要，按更新时间倒序。groupID 为空时返回助手 Run。
func (s *Store) Runs(groupID string) []RunSummary {
	s.mu.Lock()
	out := make([]RunSummary, 0)
	for _, r := range s.runs {
		if r.GroupID == groupID {
			out = append(out, runSummary(r))
		}
	}
	s.mu.Unlock()
	sortRuns(out)
	return out
}

// AllRuns 返回全部 Run 摘要，供任务中心使用。
func (s *Store) AllRuns() []RunSummary {
	s.mu.Lock()
	out := make([]RunSummary, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, runSummary(r))
	}
	s.mu.Unlock()
	sortRuns(out)
	return out
}

// GetRun 返回 Run 的副本。
func (s *Store) GetRun(id string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[id]
	if !ok {
		return Run{}, false
	}
	return r.Clone(), true
}

// SaveRun 新增或替换一个 Run 并原子写入。同一群的 Run 数量受 MaxRunsPerGroup
// 限制，已有 Run 的更新总是接受。
func (s *Store) SaveRun(r *Run) error {
	if r == nil {
		return errors.New("group: Run 为空")
	}
	if err := r.Validate(); err != nil {
		return fmt.Errorf("group: Run 非法: %w", err)
	}
	copy := r.Clone()
	copy.Schema = SchemaVersion
	s.mu.Lock()
	if _, exists := s.runs[copy.ID]; !exists {
		if copy.GroupID != "" && s.countRunsLocked(copy.GroupID) >= MaxRunsPerGroup {
			s.mu.Unlock()
			return ErrRunLimit
		}
	}
	if copy.GroupID != "" {
		if g, found := s.groups[copy.GroupID]; !found {
			copy.GroupDeleted = true
		} else if copy.GroupName == "" {
			copy.GroupName = g.Name
		}
	}
	s.runs[copy.ID] = &copy
	s.mu.Unlock()
	if s.runsDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.runsDir, storeDirMode); err != nil {
		return fmt.Errorf("group: 创建 Run 目录: %w", err)
	}
	return writeRunFile(filepath.Join(s.runsDir, copy.ID+runExt), &copy)
}

// DeleteRun 删除 Run 记录与文件。群已删除时 Run 也可以单独清理。
func (s *Store) DeleteRun(id string) error {
	s.mu.Lock()
	r, ok := s.runs[id]
	delete(s.runs, id)
	delete(s.messages, id)
	delete(s.messageSeq, id)
	s.mu.Unlock()
	if !ok {
		return ErrRunNotFound
	}
	if err := s.deleteMessageFile(r); err != nil {
		return err
	}
	if s.runsDir == "" {
		return nil
	}
	if err := os.Remove(filepath.Join(s.runsDir, id+runExt)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("group: 删除 Run 文件: %w", err)
	}
	return nil
}

func (s *Store) countRunsLocked(groupID string) int {
	n := 0
	for _, r := range s.runs {
		if r.GroupID == groupID {
			n++
		}
	}
	return n
}

func runSummary(r *Run) RunSummary {
	finished := 0
	for i := range r.Plan.Tasks {
		if r.Plan.Tasks[i].Status.Finished() {
			finished++
		}
	}
	return RunSummary{
		ID:           r.ID,
		GroupID:      r.GroupID,
		GroupName:    r.GroupName,
		Goal:         r.Goal,
		Status:       r.Status,
		Tasks:        len(r.Plan.Tasks),
		Finished:     finished,
		GroupDeleted: r.GroupDeleted,
		UpdatedAt:    r.UpdatedAt,
	}
}

func sortRuns(runs []RunSummary) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].UpdatedAt != runs[j].UpdatedAt {
			return runs[i].UpdatedAt > runs[j].UpdatedAt
		}
		return runs[i].ID < runs[j].ID
	})
}

func readGroupFile(path string) (*Group, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxGroupFileBytes {
		return nil, fmt.Errorf("文件超过 %d 字节", MaxGroupFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g Group
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, errors.New("JSON 非法")
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	g.Schema = SchemaVersion
	return &g, nil
}

func readRunFile(path string) (*Run, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxRunFileBytes {
		return nil, fmt.Errorf("文件超过 %d 字节", MaxRunFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, errors.New("JSON 非法")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	r.Schema = SchemaVersion
	return &r, nil
}

func writeGroupFile(path string, g *Group) error {
	data, err := json.Marshal(g)
	if err != nil {
		return fmt.Errorf("group: 编码群定义: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), storeDirMode); err != nil {
		return fmt.Errorf("group: 创建群目录: %w", err)
	}
	return writeFileAtomic(path, data)
}

func writeRunFile(path string, r *Run) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("group: 编码 Run: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), storeDirMode); err != nil {
		return fmt.Errorf("group: 创建 Run 目录: %w", err)
	}
	return writeFileAtomic(path, data)
}

// writeFileAtomic 在目标目录写临时文件后替换目标，避免半写入文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件: %w", err)
	}
	temp := f.Name()
	defer func() {
		if temp != "" {
			_ = os.Remove(temp)
		}
	}()
	if err := f.Chmod(storeFileMode); err != nil {
		_ = f.Close()
		return fmt.Errorf("设置文件权限: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入文件: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("同步文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭文件: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("替换文件: %w", err)
	}
	temp = ""
	return nil
}
