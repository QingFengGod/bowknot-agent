// Package group 实现群聊协作的领域模型：群、Run、计划、任务与状态，
// 以及本地持久化、计划校验、依赖调度与 Run 生命周期。
// 它不依赖界面与执行器，执行通过注入的函数完成，消息总线留给后续计划。
package group

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 文件 schema 与默认上限。schema 变更时必须递增并处理旧版本。
const (
	// SchemaVersion 是群与 Run 文件当前写出的 schema。读取时只接受 1。
	SchemaVersion = 1

	// MainAgentID 是固定群主（主 Agent）的 ID，与 agent.MainAgentID 一致。
	MainAgentID = "main"

	// MaxGroupNameRunes 限制群名长度。
	MaxGroupNameRunes = 32
	// MaxGroupMembers 限制群成员数量（FR-GRP-07）。
	MaxGroupMembers = 8
	// MaxGroups 限制保留的群数量，避免群目录无限增长。
	MaxGroups = 100
	// MaxRunsPerGroup 限制单个群保留的 Run 数量；助手 Run 不受此限。
	MaxRunsPerGroup = 200

	// MaxTasksPerRun 限制单 Run 任务数量（FR-GRP-07）。
	MaxTasksPerRun = 12
	// MaxRunGoalRunes 限制 Run 目标长度。
	MaxRunGoalRunes = 200
	// MaxTaskGoalRunes 限制任务目标长度。
	MaxTaskGoalRunes = 200
	// MaxTaskInputRunes 限制任务输入长度。
	MaxTaskInputRunes = 2000
	// MaxTaskCriteriaRunes 限制任务完成标准长度。
	MaxTaskCriteriaRunes = 200
	// MaxSummaryRunes 限制 Run 汇总长度。
	MaxSummaryRunes = 4000

	// DefaultConcurrency 是单 Run 默认并发上限（FR-GRP-07）。
	DefaultConcurrency = 3
	// MaxConcurrency 是并发上限的硬上限。
	MaxConcurrency = 3
	// DefaultMaxMessages 是单 Run 默认消息上限（FR-GRP-07）。
	DefaultMaxMessages = 200
	// DefaultMaxTaskSteps 是单任务默认步数上限（FR-GRP-07）。
	DefaultMaxTaskSteps = 20
	// DefaultWaitTimeout 是默认等待超时（FR-GRP-07）。
	DefaultWaitTimeout = 120 * time.Second

	// MaxGroupFileBytes 是单个群定义文件的最大读取体积。
	MaxGroupFileBytes = 2 << 20
	// MaxRunFileBytes 是单个 Run 文件的最大读取体积。
	MaxRunFileBytes = 2 << 20
)

// RunStatus 是 Run 的状态，对应需求 4.3。
type RunStatus string

// Run 状态。done、failed、canceled 为终态；interrupted 可由用户继续或终止。
const (
	RunPlanning       RunStatus = "planning"
	RunPendingConfirm RunStatus = "pending_confirm"
	RunRunning        RunStatus = "running"
	RunSummarizing    RunStatus = "summarizing"
	RunDone           RunStatus = "done"
	RunFailed         RunStatus = "failed"
	RunCanceled       RunStatus = "canceled"
	RunInterrupted    RunStatus = "interrupted"
)

// Text 返回 Run 状态的中文标签。
func (s RunStatus) Text() string {
	switch s {
	case RunPlanning:
		return "规划中"
	case RunPendingConfirm:
		return "待确认"
	case RunRunning:
		return "执行中"
	case RunSummarizing:
		return "汇总中"
	case RunDone:
		return "已完成"
	case RunFailed:
		return "已失败"
	case RunCanceled:
		return "已取消"
	case RunInterrupted:
		return "已中断"
	default:
		return string(s)
	}
}

// Terminal 报告状态是否为终态：已完成、已失败、已取消。已中断不算终态，
// 用户仍可继续或终止（FR-AGT-07）。
func (s RunStatus) Terminal() bool {
	switch s {
	case RunDone, RunFailed, RunCanceled:
		return true
	}
	return false
}

// TaskStatus 是 Run 内一个任务的状态，对应需求 4.3。
type TaskStatus string

// 任务状态。done 与 canceled 为终态；failed 可重试，重试生成新任务并关联原任务。
const (
	TaskWaitingDependency TaskStatus = "waiting_dependency"
	TaskPending           TaskStatus = "pending"
	TaskRunning           TaskStatus = "running"
	TaskWaitingReply      TaskStatus = "waiting_reply"
	TaskWaitingApproval   TaskStatus = "waiting_approval"
	TaskPaused            TaskStatus = "paused"
	TaskDone              TaskStatus = "done"
	TaskFailed            TaskStatus = "failed"
	TaskCanceled          TaskStatus = "canceled"
	TaskInterrupted       TaskStatus = "interrupted"
)

// Text 返回任务状态的中文标签。
func (s TaskStatus) Text() string {
	switch s {
	case TaskWaitingDependency:
		return "等待依赖"
	case TaskPending:
		return "待开始"
	case TaskRunning:
		return "执行中"
	case TaskWaitingReply:
		return "等待回复"
	case TaskWaitingApproval:
		return "等待审批"
	case TaskPaused:
		return "已暂停"
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

// Terminal 报告任务是否到达终态：已完成、已取消。
func (s TaskStatus) Terminal() bool {
	switch s {
	case TaskDone, TaskCanceled:
		return true
	}
	return false
}

// Finished 报告任务是否已结束，不再在本次 Run 内转换状态。已失败可重试，
// 但重试会创建新任务，因此对当前任务而言同样不再变化；已中断可恢复，不算结束。
func (s TaskStatus) Finished() bool {
	switch s {
	case TaskDone, TaskFailed, TaskCanceled:
		return true
	}
	return false
}

// Report 是子 Agent 返回的结构化汇报。
type Report struct {
	Conclusion   string   `json:"conclusion,omitempty"`
	Artifacts    []string `json:"artifacts,omitempty"`
	OpenIssues   []string `json:"open_issues,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Unstructured bool     `json:"unstructured"`
}

// Task 是 Run 内的最小执行单元。
type Task struct {
	ID             string     `json:"id"`
	Goal           string     `json:"goal"`
	Input          string     `json:"input,omitempty"`
	Criteria       string     `json:"criteria,omitempty"`
	AssigneeID     string     `json:"assignee_id"`
	AssigneeName   string     `json:"assignee_name"`
	DependsOn      []string   `json:"depends_on,omitempty"`
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

// Plan 是一次 Run 的任务计划。
type Plan struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Tasks      []Task `json:"tasks"`
}

// Budget 是 Run 的运行限制。零值由 DefaultBudget 填充，读取时按原值校验。
type Budget struct {
	MaxTasks           int `json:"max_tasks"`
	MaxConcurrency     int `json:"max_concurrency"`
	MaxMessages        int `json:"max_messages"`
	MaxTaskSteps       int `json:"max_task_steps"`
	WaitTimeoutSeconds int `json:"wait_timeout_seconds"`
	// TokenBudget 为 0 表示未设定，需求中仍为 [TBD]。
	TokenBudget int `json:"token_budget,omitempty"`
}

// DefaultBudget 返回 FR-GRP-07 的默认限制。
func DefaultBudget() Budget {
	return Budget{
		MaxTasks:           MaxTasksPerRun,
		MaxConcurrency:     DefaultConcurrency,
		MaxMessages:        DefaultMaxMessages,
		MaxTaskSteps:       DefaultMaxTaskSteps,
		WaitTimeoutSeconds: int(DefaultWaitTimeout / time.Second),
	}
}

// Validate 检查预算是否落在允许范围内。
func (b Budget) Validate() error {
	if b.MaxTasks < 1 || b.MaxTasks > MaxTasksPerRun {
		return fmt.Errorf("预算任务数 %d 超出 1–%d", b.MaxTasks, MaxTasksPerRun)
	}
	if b.MaxConcurrency < 1 || b.MaxConcurrency > MaxConcurrency {
		return fmt.Errorf("预算并发 %d 超出 1–%d", b.MaxConcurrency, MaxConcurrency)
	}
	if b.MaxMessages < 1 || b.MaxMessages > DefaultMaxMessages {
		return fmt.Errorf("预算消息 %d 超出 1–%d", b.MaxMessages, DefaultMaxMessages)
	}
	if b.MaxTaskSteps < 1 || b.MaxTaskSteps > DefaultMaxTaskSteps {
		return fmt.Errorf("预算步数 %d 超出 1–%d", b.MaxTaskSteps, DefaultMaxTaskSteps)
	}
	if b.WaitTimeoutSeconds < 1 || b.WaitTimeoutSeconds > int(DefaultWaitTimeout/time.Second) {
		return fmt.Errorf("预算等待超时 %d 秒超出 1–%d", b.WaitTimeoutSeconds, int(DefaultWaitTimeout/time.Second))
	}
	if b.TokenBudget < 0 {
		return errors.New("预算 token 不能为负")
	}
	return nil
}

// WaitTimeout 返回等待超时，零值回退到默认值。
func (b Budget) WaitTimeout() time.Duration {
	if b.WaitTimeoutSeconds <= 0 {
		return DefaultWaitTimeout
	}
	return time.Duration(b.WaitTimeoutSeconds) * time.Second
}

// Concurrency 返回并发上限，零值回退到默认值并受硬上限约束。
func (b Budget) Concurrency() int {
	n := b.MaxConcurrency
	if n <= 0 {
		n = DefaultConcurrency
	}
	if n > MaxConcurrency {
		n = MaxConcurrency
	}
	return n
}

// Run 是群聊或助手会话内的一次协作。
type Run struct {
	Schema int    `json:"schema"`
	ID     string `json:"id"`
	// GroupID 为空表示助手会话内的 Run。
	GroupID        string    `json:"group_id,omitempty"`
	GroupName      string    `json:"group_name,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Goal           string    `json:"goal"`
	Status         RunStatus `json:"status"`
	Plan           Plan      `json:"plan"`
	Budget         Budget    `json:"budget"`
	Summary        string    `json:"summary,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	UsageEstimated bool      `json:"usage_estimated"`
	// GroupDeleted 标记群已删除但 Run 记录保留（FR-GRP-10）。
	GroupDeleted bool  `json:"group_deleted,omitempty"`
	CreatedAt    int64 `json:"created_at"`
	UpdatedAt    int64 `json:"updated_at"`
}

// Group 是长期协作容器，群主固定为主 Agent。
type Group struct {
	Schema    int      `json:"schema"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	OwnerID   string   `json:"owner_id"`
	Members   []string `json:"members"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

// NewGroup 返回一个尚未落盘的群。
func NewGroup(now time.Time, name string, members []string) *Group {
	at := now.Unix()
	return &Group{
		Schema:    SchemaVersion,
		ID:        NewGroupID(),
		Name:      strings.TrimSpace(name),
		OwnerID:   MainAgentID,
		Members:   append([]string(nil), members...),
		CreatedAt: at,
		UpdatedAt: at,
	}
}

// NewRun 返回一个规划中的 Run。groupID 为空表示助手会话内的 Run。
func NewRun(now time.Time, groupID, groupName, conversationID, goal string) *Run {
	at := now.Unix()
	return &Run{
		Schema:         SchemaVersion,
		ID:             NewRunID(),
		GroupID:        groupID,
		GroupName:      groupName,
		ConversationID: conversationID,
		Goal:           strings.TrimSpace(goal),
		Status:         RunPlanning,
		Budget:         DefaultBudget(),
		CreatedAt:      at,
		UpdatedAt:      at,
	}
}

// NewTask 返回一个等待依赖的任务。
func NewTask(now time.Time, goal, input, criteria, assigneeID, assigneeName string, dependsOn []string) Task {
	at := now.Unix()
	return Task{
		ID:           NewTaskID(),
		Goal:         strings.TrimSpace(goal),
		Input:        strings.TrimSpace(input),
		Criteria:     strings.TrimSpace(criteria),
		AssigneeID:   assigneeID,
		AssigneeName: assigneeName,
		DependsOn:    append([]string(nil), dependsOn...),
		Status:       TaskWaitingDependency,
		CreatedAt:    at,
		UpdatedAt:    at,
	}
}

// NewGroupID 返回 gp-xxxxxxxx 形式的群 ID。
func NewGroupID() string { return newID("gp-") }

// NewRunID 返回 r-xxxxxxxx 形式的 Run ID。
func NewRunID() string { return newID("r-") }

// NewTaskID 返回 t-xxxxxxxx 形式的任务 ID。
func NewTaskID() string { return newID("t-") }

func newID(prefix string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%s%08x", prefix, time.Now().UnixNano()&0xffffffff)
	}
	return prefix + hex.EncodeToString(buf[:])
}

// Clone 返回群的深拷贝。
func (g Group) Clone() Group {
	out := g
	out.Members = append([]string(nil), g.Members...)
	return out
}

// Clone 返回 Run 的深拷贝。
func (r Run) Clone() Run {
	out := r
	out.Plan.Tasks = make([]Task, len(r.Plan.Tasks))
	for i := range r.Plan.Tasks {
		out.Plan.Tasks[i] = r.Plan.Tasks[i].Clone()
	}
	return out
}

// Clone 返回任务的深拷贝。
func (t Task) Clone() Task {
	out := t
	out.DependsOn = append([]string(nil), t.DependsOn...)
	if t.Report != nil {
		report := *t.Report
		report.Artifacts = append([]string(nil), t.Report.Artifacts...)
		report.OpenIssues = append([]string(nil), t.Report.OpenIssues...)
		out.Report = &report
	}
	return out
}

// Task 返回指定 ID 的任务。
func (r *Run) Task(id string) (*Task, bool) {
	for i := range r.Plan.Tasks {
		if r.Plan.Tasks[i].ID == id {
			return &r.Plan.Tasks[i], true
		}
	}
	return nil, false
}

// Active 返回尚未结束的任务数量。
func (r *Run) Active() int {
	n := 0
	for i := range r.Plan.Tasks {
		if !r.Plan.Tasks[i].Status.Finished() && r.Plan.Tasks[i].Status != TaskInterrupted {
			n++
		}
	}
	return n
}

// AllFinished 报告是否所有任务都已结束（已完成、失败或已取消）。
func (r *Run) AllFinished() bool {
	if len(r.Plan.Tasks) == 0 {
		return false
	}
	for i := range r.Plan.Tasks {
		if !r.Plan.Tasks[i].Status.Finished() {
			return false
		}
	}
	return true
}

// DerivedStatus 在调用方没有更精确状态时，按任务状态推导 Run 状态。
// 规划中、待确认保持原状态；仍有活动任务时为执行中；未结束任务全部为已中断
// 时为已中断；所有任务结束后按已完成、已失败、已取消的优先级推导。
func (r *Run) DerivedStatus() RunStatus {
	if len(r.Plan.Tasks) == 0 {
		return r.Status
	}
	if r.Status == RunPlanning || r.Status == RunPendingConfirm {
		return r.Status
	}
	anyDone, anyFailed, anyCanceled, anyInterrupted, anyActive := false, false, false, false, false
	for i := range r.Plan.Tasks {
		switch r.Plan.Tasks[i].Status {
		case TaskDone:
			anyDone = true
		case TaskFailed:
			anyFailed = true
		case TaskCanceled:
			anyCanceled = true
		case TaskInterrupted:
			anyInterrupted = true
		default:
			anyActive = true
		}
	}
	if anyActive {
		return RunRunning
	}
	if anyInterrupted {
		return RunInterrupted
	}
	switch {
	case anyDone:
		return RunDone
	case anyFailed:
		return RunFailed
	case anyCanceled:
		return RunCanceled
	default:
		return RunCanceled
	}
}

// Touch 更新 Run 的更新时间。
func (r *Run) Touch(now time.Time) { r.UpdatedAt = now.Unix() }

// Validate 检查群定义。读取失败的文件由 store 跳过，不删除。
func (g Group) Validate() error {
	if g.Schema < 1 || g.Schema > SchemaVersion {
		return fmt.Errorf("schema %d 不受支持", g.Schema)
	}
	if !ValidGroupID(g.ID) {
		return errors.New("群 ID 非法")
	}
	if err := checkRunes("群名", g.Name, 1, MaxGroupNameRunes); err != nil {
		return err
	}
	if g.OwnerID != MainAgentID {
		return fmt.Errorf("群主 %q 非法，必须为主 Agent", g.OwnerID)
	}
	if len(g.Members) > MaxGroupMembers {
		return fmt.Errorf("群成员 %d 个，超过上限 %d", len(g.Members), MaxGroupMembers)
	}
	seen := map[string]bool{}
	for _, id := range g.Members {
		if !ValidSubAgentID(id) {
			return fmt.Errorf("群成员 ID %q 非法", id)
		}
		if seen[id] {
			return fmt.Errorf("群成员 %s 重复", id)
		}
		seen[id] = true
	}
	if g.CreatedAt <= 0 || g.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	return nil
}

// Validate 检查 Run 文件。规划中的 Run 允许暂时没有计划。
func (r Run) Validate() error {
	if r.Schema < 1 || r.Schema > SchemaVersion {
		return fmt.Errorf("schema %d 不受支持", r.Schema)
	}
	if !ValidRunID(r.ID) {
		return errors.New("Run ID 非法")
	}
	if r.GroupID != "" && !ValidGroupID(r.GroupID) {
		return errors.New("Run 所属群 ID 非法")
	}
	if r.GroupID == "" && strings.TrimSpace(r.ConversationID) == "" {
		return errors.New("Run 缺少归属：群或会话")
	}
	if err := checkRunes("目标", r.Goal, 1, MaxRunGoalRunes); err != nil {
		return err
	}
	if !validRunStatus(r.Status) {
		return fmt.Errorf("Run 状态 %q 非法", r.Status)
	}
	if err := r.Budget.Validate(); err != nil {
		return err
	}
	if err := checkRunes("汇总", r.Summary, 0, MaxSummaryRunes); err != nil {
		return err
	}
	if len(r.Plan.Tasks) == 0 {
		if r.Status != RunPlanning {
			return errors.New("计划为空")
		}
	} else if err := ValidatePlan(r.Plan, r.Budget); err != nil {
		return err
	}
	if r.CreatedAt <= 0 || r.UpdatedAt <= 0 {
		return errors.New("时间戳非法")
	}
	return nil
}

// Validate 检查一个任务。依赖是否成环由 ValidatePlan 负责。
func (t Task) Validate() error {
	if !ValidTaskID(t.ID) {
		return errors.New("任务 ID 非法")
	}
	if err := checkRunes("目标", t.Goal, 1, MaxTaskGoalRunes); err != nil {
		return err
	}
	if err := checkRunes("输入", t.Input, 0, MaxTaskInputRunes); err != nil {
		return err
	}
	if err := checkRunes("完成标准", t.Criteria, 0, MaxTaskCriteriaRunes); err != nil {
		return err
	}
	if !ValidSubAgentID(t.AssigneeID) {
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
	for _, dep := range t.DependsOn {
		if !ValidTaskID(dep) {
			return fmt.Errorf("依赖 ID %q 非法", dep)
		}
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

func validRunStatus(s RunStatus) bool {
	switch s {
	case RunPlanning, RunPendingConfirm, RunRunning, RunSummarizing, RunDone, RunFailed, RunCanceled, RunInterrupted:
		return true
	}
	return false
}

func validTaskStatus(s TaskStatus) bool {
	switch s {
	case TaskWaitingDependency, TaskPending, TaskRunning, TaskWaitingReply, TaskWaitingApproval,
		TaskPaused, TaskDone, TaskFailed, TaskCanceled, TaskInterrupted:
		return true
	}
	return false
}

// ValidGroupID 报告 gp- 加 8 位小写十六进制，可安全作为目录名。
func ValidGroupID(id string) bool { return validHexID(id, "gp-") }

// ValidRunID 报告 r- 加 8 位小写十六进制，可安全作为文件名。
func ValidRunID(id string) bool { return validHexID(id, "r-") }

// ValidTaskID 报告 t- 加 8 位小写十六进制。
func ValidTaskID(id string) bool { return validHexID(id, "t-") }

// ValidSubAgentID 报告 a- 加 8 位小写十六进制，与 agent 包的子 Agent ID 一致。
func ValidSubAgentID(id string) bool { return validHexID(id, "a-") }

func validHexID(id, prefix string) bool {
	if len(id) != len(prefix)+8 || !strings.HasPrefix(id, prefix) {
		return false
	}
	for _, r := range id[len(prefix):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
