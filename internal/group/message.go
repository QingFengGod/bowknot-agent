package group

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// 消息文件的布局与上限（FR-GRP-02、FR-GRP-07）。
const (
	// MaxMessageRunes 限制单条消息正文长度。
	MaxMessageRunes = 2000
	// MaxMessageFileBytes 是单个消息文件的最大读取体积。
	MaxMessageFileBytes = 8 << 20
	// MaxMessageLineBytes 限制一行 JSONL 的最大读取体积。
	MaxMessageLineBytes = 64 << 10

	messageExt      = ".jsonl"
	messageDirName  = "messages"
	messageFileMode = 0o600
	messageDirMode  = 0o700
	messageIDSuffix = 8
	messageIDPrefix = "m-"
)

// MessageType 是群消息的固定类型（需求 4.3）。
type MessageType string

// 消息类型。所有类型的消息都对用户与全部成员可见；定向请求只是额外写入
// ToID，不构成私聊旁路。
const (
	MessageProgress MessageType = "progress"
	MessageRequest  MessageType = "request"
	MessageResponse MessageType = "response"
	MessageHandoff  MessageType = "handoff"
	MessageBlocked  MessageType = "blocked"
	MessageReport   MessageType = "report"
)

// Text 返回消息类型的中文标签。
func (t MessageType) Text() string {
	switch t {
	case MessageProgress:
		return "进展"
	case MessageRequest:
		return "请求"
	case MessageResponse:
		return "答复"
	case MessageHandoff:
		return "交接"
	case MessageBlocked:
		return "阻塞"
	case MessageReport:
		return "汇报"
	default:
		return string(t)
	}
}

// Message 是 Run 内的一条群消息。Seq 在同一个 Run 内单调递增，用于稳定
// 排序与等待游标。
type Message struct {
	ID      string      `json:"id"`
	RunID   string      `json:"run_id"`
	TaskID  string      `json:"task_id,omitempty"`
	FromID  string      `json:"from_id"`
	ToID    string      `json:"to_id,omitempty"`
	Type    MessageType `json:"type"`
	Content string      `json:"content"`
	ReplyTo string      `json:"reply_to,omitempty"`
	Seq     int         `json:"seq"`
	At      int64       `json:"at"`
}

// NewMessageID 返回 m-xxxxxxxx 形式的消息 ID。
func NewMessageID() string { return newID(messageIDPrefix) }

// ErrMessageLimit 表示该 Run 的消息已达预算上限（FR-GRP-07）。
var ErrMessageLimit = fmt.Errorf("消息已达上限 %d，协作已暂停", DefaultMaxMessages)

// ErrMessageWrite 表示消息已保留在内存，但写入消息文件失败（FR-GRP-02）。
var ErrMessageWrite = errors.New("群消息写入失败")

// Validate 检查消息字段。tasks 的 ID 是否真实存在由 Bus 调用方保证。
func (m Message) Validate() error {
	if !validMessageID(m.ID) {
		return errors.New("消息 ID 非法")
	}
	if !ValidRunID(m.RunID) {
		return errors.New("消息所属 Run ID 非法")
	}
	if m.TaskID != "" && !ValidTaskID(m.TaskID) {
		return errors.New("消息任务 ID 非法")
	}
	if !ValidAuthorID(m.FromID) {
		return fmt.Errorf("消息发送者 %q 非法", m.FromID)
	}
	if m.ToID != "" && !ValidAuthorID(m.ToID) {
		return fmt.Errorf("消息接收者 %q 非法", m.ToID)
	}
	if !validMessageType(m.Type) {
		return fmt.Errorf("消息类型 %q 非法", m.Type)
	}
	if err := checkRunes("消息正文", m.Content, 1, MaxMessageRunes); err != nil {
		return err
	}
	if m.ReplyTo != "" && !validMessageID(m.ReplyTo) {
		return errors.New("答复目标消息 ID 非法")
	}
	if m.Type == MessageRequest && m.ToID == "" {
		return errors.New("请求消息缺少接收者")
	}
	if m.Type == MessageResponse && (m.ToID == "" || m.ReplyTo == "") {
		return errors.New("答复消息缺少接收者或原请求")
	}
	if m.Seq < 0 {
		return errors.New("消息序号不能为负")
	}
	if m.At <= 0 {
		return errors.New("消息时间戳非法")
	}
	return nil
}

// ValidAuthorID 报告 id 是否为主 Agent 或子 Agent。
func ValidAuthorID(id string) bool {
	return id == MainAgentID || ValidSubAgentID(id)
}

func validMessageType(t MessageType) bool {
	switch t {
	case MessageProgress, MessageRequest, MessageResponse, MessageHandoff, MessageBlocked, MessageReport:
		return true
	}
	return false
}

func validMessageID(id string) bool {
	if len(id) != len(messageIDPrefix)+messageIDSuffix || !strings.HasPrefix(id, messageIDPrefix) {
		return false
	}
	return validHex(id[len(messageIDPrefix):])
}

func validHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// AppendMessage 追加一条消息到 Run 的消息文件并分配序号。写入磁盘失败时
// 消息仍保留在内存，返回已赋值序号的副本与错误，调用方可以提示但不阻塞
// 协作（FR-GRP-02）。消息超限或字段非法时返回零值与错误。
func (s *Store) AppendMessage(m Message) (Message, error) {
	if m.ID == "" {
		m.ID = NewMessageID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[m.RunID]
	if !ok {
		return Message{}, ErrRunNotFound
	}
	m.Seq = s.messageSeq[m.RunID] + 1
	if m.At <= 0 {
		m.At = s.now().Unix()
	}
	if err := m.Validate(); err != nil {
		return Message{}, fmt.Errorf("group: 消息非法: %w", err)
	}
	limit := r.Budget.MaxMessages
	if limit <= 0 || limit > DefaultMaxMessages {
		limit = DefaultMaxMessages
	}
	if len(s.messages[m.RunID]) >= limit {
		return Message{}, fmt.Errorf("%w（Run %s）", ErrMessageLimit, m.RunID)
	}
	path := s.messagesPathLocked(r)
	if path != "" {
		if err := appendMessageLine(path, m); err != nil {
			s.messages[m.RunID] = append(s.messages[m.RunID], m)
			s.messageSeq[m.RunID] = m.Seq
			return m, fmt.Errorf("%w：%v", ErrMessageWrite, err)
		}
	}
	s.messages[m.RunID] = append(s.messages[m.RunID], m)
	s.messageSeq[m.RunID] = m.Seq
	return m, nil
}

// Messages 返回一个 Run 的消息快照，按追加顺序排列。
func (s *Store) Messages(runID string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Message(nil), s.messages[runID]...)
	return out
}

// MessageCount 返回一个 Run 已保存的消息数量。
func (s *Store) MessageCount(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages[runID])
}

// LastSeq 返回一个 Run 已保存消息的最大序号，没有消息时为 0。
func (s *Store) LastSeq(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageSeq[runID]
}

// messagesPathLocked 返回 Run 的消息文件路径：群 Run 放在群目录内，删除群时
// 一并删除；助手 Run 放在 runs/ 下，与 Run 文件相邻。空目录只在内存保存。
func (s *Store) messagesPathLocked(r *Run) string {
	if r.GroupID != "" {
		if s.groupsDir == "" {
			return ""
		}
		return filepath.Join(s.groupsDir, r.GroupID, messageDirName, r.ID+messageExt)
	}
	if s.runsDir == "" {
		return ""
	}
	return filepath.Join(s.runsDir, r.ID+messageExt)
}

// loadMessages 读取已知 Run 的消息文件。损坏、超限与字段非法的行只记录并
// 跳过；缺失文件视为空。消息数量超过预算时只保留前 MaxMessages 条。
func (s *Store) loadMessages(runs map[string]*Run) map[string][]Message {
	out := make(map[string][]Message, len(runs))
	if s.groupsDir == "" && s.runsDir == "" {
		return out
	}
	for id, r := range runs {
		path := s.messagesPathFor(r)
		if path == "" {
			continue
		}
		msgs, err := readMessageFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("group: 跳过消息 %s: %v", path, err)
			}
			continue
		}
		limit := r.Budget.MaxMessages
		if limit <= 0 || limit > DefaultMaxMessages {
			limit = DefaultMaxMessages
		}
		if len(msgs) > limit {
			msgs = msgs[:limit]
		}
		for i := range msgs {
			if msgs[i].RunID != id {
				log.Printf("group: 跳过消息 %s: Run 不匹配", msgs[i].ID)
				msgs = append(msgs[:i], msgs[i+1:]...)
				i--
			}
		}
		out[id] = msgs
	}
	return out
}

// messagesPathFor 与 messagesPathLocked 相同，但在 Load 阶段不持锁。
func (s *Store) messagesPathFor(r *Run) string {
	if r.GroupID != "" {
		if s.groupsDir == "" {
			return ""
		}
		return filepath.Join(s.groupsDir, r.GroupID, messageDirName, r.ID+messageExt)
	}
	if s.runsDir == "" {
		return ""
	}
	return filepath.Join(s.runsDir, r.ID+messageExt)
}

func appendMessageLine(path string, m Message) error {
	if err := os.MkdirAll(filepath.Dir(path), messageDirMode); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, messageFileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func readMessageFile(path string) ([]Message, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxMessageFileBytes {
		return nil, fmt.Errorf("消息文件超过 %d 字节", MaxMessageFileBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), MaxMessageLineBytes)
	out := make([]Message, 0, DefaultMaxMessages)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			log.Printf("group: 跳过损坏消息行 %s: %v", path, err)
			continue
		}
		if err := m.Validate(); err != nil {
			log.Printf("group: 跳过非法消息 %s: %v", path, err)
			continue
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// deleteMessageFile 删除一个 Run 的消息文件；文件不存在不算失败。
func (s *Store) deleteMessageFile(r *Run) error {
	path := s.messagesPathFor(r)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("group: 删除消息文件: %w", err)
	}
	return nil
}

// messageSeqOf 返回消息的最大序号；消息为空时返回 0。
func messageSeqOf(msgs []Message) int {
	maxSeq := 0
	for i := range msgs {
		if msgs[i].Seq > maxSeq {
			maxSeq = msgs[i].Seq
		}
	}
	return maxSeq
}

func truncateRunes(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return string(runes[:n])
}

// MessageLimit 返回一个 Run 生效的消息上限。
func (s *Store) MessageLimit(runID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runID]
	if !ok {
		return DefaultMaxMessages
	}
	if r.Budget.MaxMessages <= 0 || r.Budget.MaxMessages > DefaultMaxMessages {
		return DefaultMaxMessages
	}
	return r.Budget.MaxMessages
}
