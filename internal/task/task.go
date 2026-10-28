// Package task 实现任务组与任务的数据结构、状态机、校验与本地持久化。
// 它不依赖任何业务包，调度通过注入的执行函数完成。
package task

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 任务文件的结构与上限。schema 变更时必须递增并处理旧版本。
const (
	// SchemaVersion is the task file schema this build reads.
	SchemaVersion = 1
	// MaxTasksPerGroup caps the tasks of one plan.
	MaxTasksPerGroup = 5
	// MaxConcurrentTasks is how many tasks of one group run at once.
	MaxConcurrentTasks = 2
	// MaxTaskFileBytes is the largest task file the store loads.
	MaxTaskFileBytes = 2 << 20
	// MaxGoalRunes caps the goal of one task.
	MaxGoalRunes = 200
	// MaxInputRunes caps the input of one task.
	MaxInputRunes = 2000
	// MaxCriteriaRunes caps the completion criteria of one task.
	MaxCriteriaRunes = 200
	// PlanTimeout bounds the planning call.
	PlanTimeout = 60 * time.Second
	// TaskTimeout bounds one task execution.
	TaskTimeout = 120 * time.Second
	// SummaryTimeout bounds the summary call.
	SummaryTimeout = 120 * time.Second
)

// GroupStatus is the status of one task group.
type GroupStatus string

// Group statuses. done, failed, canceled and interrupted are terminal.
const (
	GroupPending     GroupStatus = "pending"
	GroupRunning     GroupStatus = "running"
	GroupSummarizing GroupStatus = "summarizing"
	GroupDone        GroupStatus = "done"
	GroupCanceled    GroupStatus = "canceled"
	GroupFailed      GroupStatus = "failed"
	GroupInterrupted GroupStatus = "interrupted"
)

// Text returns the Chinese label of the group status.
func (s GroupStatus) Text() string {
	switch s {
	case GroupPending:
		return "待确认"
	case GroupRunning:
		return "执行中"
	case GroupSummarizing:
		return "汇总中"
	case GroupDone:
		return "已完成"
	case GroupCanceled:
		return "已取消"
	case GroupFailed:
		return "已失败"
	case GroupInterrupted:
		return "已中断"
	default:
		return string(s)
	}
}

// Terminal reports whether the status never changes again.
func (s GroupStatus) Terminal() bool {
	switch s {
	case GroupDone, GroupFailed, GroupCanceled, GroupInterrupted:
		return true
	}
	return false
}

// TaskStatus is the status of one task inside a group.
type TaskStatus string

// Task statuses. done, failed, canceled and interrupted are terminal.
const (
	TaskPending     TaskStatus = "pending"
	TaskRunning     TaskStatus = "running"
	TaskDone        TaskStatus = "done"
	TaskFailed      TaskStatus = "failed"
	TaskCanceled    TaskStatus = "canceled"
	TaskInterrupted TaskStatus = "interrupted"
)

// Text returns the Chinese label of the task status.
func (s TaskStatus) Text() string {
	switch s {
	case TaskPending:
		return "待开始"
	case TaskRunning:
		return "执行中"
	case TaskDone:
		return "已完成"
	case TaskFailed:
		return "已失败"
	case TaskCanceled:
		return "已取消"
	case TaskInterrupted:
		return "已中断"
	default:
		return string(s)
	}
}

// Terminal reports whether the status never changes again.
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskDone, TaskFailed, TaskCanceled, TaskInterrupted:
		return true
	}
	return false
}

// Report is the structured outcome one subtask returns.
type Report struct {
	Conclusion   string   `json:"conclusion,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"`
	OpenIssues   []string `json:"open_issues,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Unstructured bool     `json:"unstructured"`
}

// Task is one unit of work assigned to a sub agent.
type Task struct {
	ID             string     `json:"id"`
	Goal           string     `json:"goal"`
	Input          string     `json:"input"`
	Criteria       string     `json:"criteria"`
	AssigneeID     string     `json:"assignee_id"`
	AssigneeName   string     `json:"assignee_name"`
	Status         TaskStatus `json:"status"`
	Report         *Report    `json:"report,omitempty"`
	InputTokens    int        `json:"input_tokens"`
	OutputTokens   int        `json:"output_tokens"`
	UsageEstimated bool       `json:"usage_estimated"`
	DurationMS     int64      `json:"duration_ms"`
	ErrorCode      string     `json:"error_code,omitempty"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	RetryOf        string     `json:"retry_of,omitempty"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
}

// Group is one plan of tasks belonging to one conversation message.
type Group struct {
	Schema         int         `json:"schema"`
	ID             string      `json:"id"`
	ConversationID string      `json:"conversation_id"`
	MessageIndex   int         `json:"message_index"`
	ProviderID     string      `json:"provider_id"`
	Model          string      `json:"model"`
	Status         GroupStatus `json:"status"`
	CreatedAt      int64       `json:"created_at"`
	UpdatedAt      int64       `json:"updated_at"`
	Tasks          []Task      `json:"tasks"`
}

// NewGroup returns an empty plan awaiting confirmation.
func NewGroup(now time.Time, conversationID string, messageIndex int, providerID, model string) *Group {
	at := now.Unix()
	return &Group{
		Schema:         SchemaVersion,
		ID:             NewGroupID(),
		ConversationID: conversationID,
		MessageIndex:   messageIndex,
		ProviderID:     providerID,
		Model:          model,
		Status:         GroupPending,
		CreatedAt:      at,
		UpdatedAt:      at,
	}
}

// NewTask returns a pending task.
func NewTask(now time.Time, goal, input, criteria, assigneeID, assigneeName string) Task {
	at := now.Unix()
	return Task{
		ID:           NewTaskID(),
		Goal:         strings.TrimSpace(goal),
		Input:        strings.TrimSpace(input),
		Criteria:     strings.TrimSpace(criteria),
		AssigneeID:   assigneeID,
		AssigneeName: assigneeName,
		Status:       TaskPending,
		CreatedAt:    at,
		UpdatedAt:    at,
	}
}

// NewGroupID returns a group ID of the form g-xxxxxxxx.
func NewGroupID() string { return newID("g-") }

// NewTaskID returns a task ID of the form t-xxxxxxxx.
func NewTaskID() string { return newID("t-") }

func newID(prefix string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s%08x", prefix, time.Now().UnixNano()&0xffffffff)
	}
	return prefix + hex.EncodeToString(buf[:])
}

// Clone returns a deep copy of g.
func (g Group) Clone() Group {
	out := g
	out.Tasks = make([]Task, len(g.Tasks))
	for i := range g.Tasks {
		out.Tasks[i] = g.Tasks[i].Clone()
	}
	return out
}

// Clone returns a deep copy of t.
func (t Task) Clone() Task {
	out := t
	if t.Report != nil {
		report := *t.Report
		report.Artifacts = append([]string(nil), t.Report.Artifacts...)
		report.OpenIssues = append([]string(nil), t.Report.OpenIssues...)
		out.Report = &report
	}
	return out
}

// Task returns the task with the ID, or false.
func (g *Group) Task(id string) (*Task, bool) {
	for i := range g.Tasks {
		if g.Tasks[i].ID == id {
			return &g.Tasks[i], true
		}
	}
	return nil, false
}

// AllTerminal reports whether every task reached a terminal status.
func (g *Group) AllTerminal() bool {
	for i := range g.Tasks {
		if !g.Tasks[i].Status.Terminal() {
			return false
		}
	}
	return len(g.Tasks) > 0
}

// DerivedStatus summarizes the task statuses when the caller has no richer
// state: canceled when all are canceled, failed when all failed, done when at
// least one succeeded, running while work is in flight, pending otherwise.
func (g *Group) DerivedStatus() GroupStatus {
	if len(g.Tasks) == 0 {
		return g.Status
	}
	allTerminal, anyDone, anyFailed, anyCanceled := true, false, false, false
	for i := range g.Tasks {
		t := &g.Tasks[i]
		if !t.Status.Terminal() {
			allTerminal = false
		}
		switch t.Status {
		case TaskDone:
			anyDone = true
		case TaskFailed:
			anyFailed = true
		case TaskCanceled:
			anyCanceled = true
		}
	}
	if !allTerminal {
		if g.Status == GroupPending {
			return GroupPending
		}
		return GroupRunning
	}
	switch {
	case anyDone:
		return GroupDone
	case anyFailed:
		return GroupFailed
	case anyCanceled:
		return GroupCanceled
	default:
		return GroupInterrupted
	}
}

// MarkInterrupted moves a group left running by a crash to interrupted.
// Tasks still pending or running become interrupted too; done, failed and
// canceled tasks keep their outcome. A group still awaiting confirmation is
// left untouched, so the user can confirm it after the restart.
func (g *Group) MarkInterrupted(now time.Time) {
	if g.Status != GroupRunning && g.Status != GroupSummarizing {
		return
	}
	at := now.Unix()
	for i := range g.Tasks {
		switch g.Tasks[i].Status {
		case TaskPending, TaskRunning:
			g.Tasks[i].Status = TaskInterrupted
			g.Tasks[i].UpdatedAt = at
		}
	}
	g.Status = GroupInterrupted
	g.UpdatedAt = at
}

// CancelPending marks every task that has not started as canceled.
func (g *Group) CancelPending(now time.Time) {
	at := now.Unix()
	for i := range g.Tasks {
		if g.Tasks[i].Status == TaskPending {
			g.Tasks[i].Status = TaskCanceled
			g.Tasks[i].UpdatedAt = at
		}
	}
	g.UpdatedAt = at
}

// Touch moves the group update time.
func (g *Group) Touch(now time.Time) { g.UpdatedAt = now.Unix() }

// Validate checks a group read from disk. A file that fails is skipped by the
// store and never deleted.
func (g *Group) Validate() error {
	if g.Schema != SchemaVersion {
		return fmt.Errorf("schema %d 不受支持", g.Schema)
	}
	if !ValidGroupID(g.ID) {
		return errors.New("任务组 ID 非法")
	}
	if strings.TrimSpace(g.ConversationID) == "" {
		return errors.New("会话 ID 为空")
	}
	if g.MessageIndex < 0 {
		return errors.New("消息索引非法")
	}
	if !validGroupStatus(g.Status) {
		return fmt.Errorf("任务组状态 %q 非法", g.Status)
	}
	if g.CreatedAt <= 0 || g.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	if strings.TrimSpace(g.ProviderID) == "" || strings.TrimSpace(g.Model) == "" {
		return errors.New("规划模型为空")
	}
	if len(g.Tasks) == 0 || len(g.Tasks) > MaxTasksPerGroup {
		return fmt.Errorf("任务 %d 条，超出 1–%d", len(g.Tasks), MaxTasksPerGroup)
	}
	seen := map[string]bool{}
	for i := range g.Tasks {
		if err := g.Tasks[i].Validate(); err != nil {
			return fmt.Errorf("任务 %d：%w", i+1, err)
		}
		if seen[g.Tasks[i].ID] {
			return fmt.Errorf("任务 ID %s 重复", g.Tasks[i].ID)
		}
		seen[g.Tasks[i].ID] = true
	}
	return nil
}

// Validate checks one task read from disk.
func (t Task) Validate() error {
	if !ValidTaskID(t.ID) {
		return errors.New("任务 ID 非法")
	}
	if err := checkRunes("目标", t.Goal, 1, MaxGoalRunes); err != nil {
		return err
	}
	if err := checkRunes("输入", t.Input, 0, MaxInputRunes); err != nil {
		return err
	}
	if err := checkRunes("完成标准", t.Criteria, 0, MaxCriteriaRunes); err != nil {
		return err
	}
	if !validAssigneeID(t.AssigneeID) {
		return errors.New("执行者 ID 非法")
	}
	if strings.TrimSpace(t.AssigneeName) == "" {
		return errors.New("执行者名称为空")
	}
	if !validTaskStatus(t.Status) {
		return fmt.Errorf("任务状态 %q 非法", t.Status)
	}
	if t.CreatedAt <= 0 || t.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	if t.RetryOf != "" && !ValidTaskID(t.RetryOf) {
		return errors.New("重试来源 ID 非法")
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

func validGroupStatus(s GroupStatus) bool {
	switch s {
	case GroupPending, GroupRunning, GroupSummarizing, GroupDone, GroupCanceled, GroupFailed, GroupInterrupted:
		return true
	}
	return false
}

func validTaskStatus(s TaskStatus) bool {
	switch s {
	case TaskPending, TaskRunning, TaskDone, TaskFailed, TaskCanceled, TaskInterrupted:
		return true
	}
	return false
}

// ValidGroupID reports whether id is safe as a file name: g- plus eight
// lowercase hexadecimal digits.
func ValidGroupID(id string) bool { return validHexID(id, "g-") }

// ValidTaskID reports whether id has the form t-xxxxxxxx.
func ValidTaskID(id string) bool { return validHexID(id, "t-") }

func validAssigneeID(id string) bool { return validHexID(id, "a-") }

func validHexID(id, prefix string) bool {
	if len(id) != 10 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, r := range id[2:] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
