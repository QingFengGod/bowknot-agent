package group

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

// 规划调用限制。
const (
	planningTokens    = 1600
	planningTimeout   = 60 * time.Second
	planningTaskGoal  = MaxTaskGoalRunes
	planningTaskInput = 800
)

// PlanRequest 是一次自动派发的输入。GroupID 为空表示助手会话内的 Run。
type PlanRequest struct {
	GroupID        string
	GroupName      string
	ConversationID string
	Goal           string
	ProviderID     string
	Model          string
}

// PlanResult 是自动派发的结果：要么创建待确认的 Run，要么直接给出回答。
type PlanResult struct {
	Run    *Run
	Answer string
}

// Plan 让主 Agent 判断是否需要调用已启用的子 Agent。需要时创建待确认 Run，
// 不需要时返回直接回答。群聊 Run 只能派发给群成员；助手 Run 可派发给所有
// 启用的子 Agent。规划前会执行记忆日期检查（由 AgentProvider.Prompt 完成）。
func (m *Manager) Plan(ctx context.Context, req PlanRequest) (PlanResult, error) {
	if m.Agents == nil || m.Chat == nil {
		return PlanResult{}, errors.New("group: 规划器未配置")
	}
	main, ok := m.Agents.Agent(MainAgentID)
	if !ok || !main.Enabled {
		return PlanResult{}, errors.New("group: 主 Agent 不可用")
	}
	providerID, model := req.ProviderID, req.Model
	if providerID == "" {
		providerID = main.ProviderID
	}
	if model == "" {
		model = main.Model
	}
	candidates, err := m.candidates(req.GroupID)
	if err != nil {
		return PlanResult{}, err
	}
	if len(candidates) == 0 {
		return PlanResult{}, errors.New("group: 没有可用的子 Agent")
	}

	persona, _ := m.Agents.Prompt(MainAgentID, req.Goal)
	system := persona + "\n\n" + planningRules(candidates)
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, planningTimeout)
	defer cancel()
	resp, err := m.Chat(ctx, gateway.ChatRequest{
		ProviderID:     providerID,
		Model:          model,
		ConversationID: req.ConversationID,
		Messages: []gateway.ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: "用户请求：" + req.Goal},
		},
		MaxTokens: planningTokens,
	})
	if err != nil {
		return PlanResult{}, err
	}
	action, err := parseAction(strings.TrimSpace(resp.Content))
	if err != nil {
		// 规划输出不是 JSON 时按直接回答处理，避免吞掉主 Agent 的回复。
		answer := strings.TrimSpace(resp.Content)
		if answer == "" {
			return PlanResult{}, errors.New("group: 主 Agent 未返回内容")
		}
		return PlanResult{Answer: answer}, nil
	}
	if action.Action == "answer" || (action.Action == "" && len(action.Tasks) == 0 && action.Content != "") {
		answer := strings.TrimSpace(action.Content)
		if answer == "" {
			return PlanResult{}, errors.New("group: 直接回答为空")
		}
		return PlanResult{Answer: answer}, nil
	}
	tasks := action.tasks()
	if len(tasks) == 0 {
		return PlanResult{}, errors.New("group: 派发计划没有任务")
	}

	now := m.now()
	run := NewRun(now, req.GroupID, req.GroupName, req.ConversationID, req.Goal)
	built := make([]Task, 0, len(tasks))
	for i := range tasks {
		spec, ok := m.Agents.Agent(tasks[i].AssigneeID)
		if !ok || !spec.Enabled {
			return PlanResult{}, fmt.Errorf("group: 任务 %d 的执行者 %q 不可用", i+1, tasks[i].AssigneeID)
		}
		if !containsID(candidates, tasks[i].AssigneeID) {
			return PlanResult{}, fmt.Errorf("group: 任务 %d 的执行者 %q 不在可派发范围", i+1, tasks[i].AssigneeID)
		}
		deps := make([]string, 0, len(tasks[i].deps()))
		for _, index := range tasks[i].deps() {
			if index < 0 || index >= len(tasks) || index == i {
				return PlanResult{}, fmt.Errorf("group: 任务 %d 的依赖下标 %d 非法", i+1, index)
			}
			deps = append(deps, built[index].ID)
		}
		built = append(built, NewTask(
			now,
			clipText(tasks[i].Goal, planningTaskGoal),
			clipText(tasks[i].Input, planningTaskInput),
			clipText(tasks[i].Criteria, MaxTaskCriteriaRunes),
			spec.ID,
			spec.Name,
			deps,
		))
	}
	plan := Plan{ProviderID: providerID, Model: model, Tasks: built}
	if err := run.SetPlan(now, plan); err != nil {
		return PlanResult{}, err
	}
	if err := m.Store.SaveRun(run); err != nil {
		return PlanResult{}, err
	}
	if m.OnRunCreated != nil {
		m.OnRunCreated(run.Clone())
	}
	m.notify(*run)
	return PlanResult{Run: run}, nil
}

// candidates 返回可派发的子 Agent：群 Run 取群成员，助手 Run 取全部启用成员。
func (m *Manager) candidates(groupID string) ([]string, error) {
	var ids []string
	if groupID != "" {
		g, ok := m.Store.GetGroup(groupID)
		if !ok {
			return nil, ErrGroupNotFound
		}
		ids = g.Members
	} else {
		ids = m.Agents.AgentIDs()
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !ValidSubAgentID(id) {
			continue
		}
		if spec, ok := m.Agents.Agent(id); ok && spec.Enabled {
			out = append(out, id)
		}
	}
	return out, nil
}

func planningRules(candidates []string) string {
	var b strings.Builder
	b.WriteString("你是本次协作的主 Agent，负责判断是否需要派发任务给子 Agent。每一步只输出一个 JSON 对象，不要解释。\n")
	b.WriteString("如果不需要派发，输出：{\"action\":\"answer\",\"content\":\"给用户的完整回答\"}\n")
	b.WriteString("如果需要派发，输出：{\"action\":\"delegate\",\"tasks\":[{\"goal\":\"任务目标\",\"input\":\"任务输入\",\"criteria\":\"完成标准\",\"assignee_id\":\"子 Agent ID\",\"depends_on_indexes\":[0]}]}\n")
	b.WriteString("depends_on_indexes 是同一计划内前置任务的下标（从 0 开始），没有依赖时省略。任务目标最多 200 字。\n")
	b.WriteString("只能派发给已启用的子 Agent，不得编造 ID。\n\n可派发的子 Agent：\n")
	for _, id := range candidates {
		b.WriteString("- " + id + "\n")
	}
	return b.String()
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

type planBodyJSON struct {
	Tasks []planTaskJSON `json:"tasks"`
}

type planTaskJSON struct {
	Goal             string `json:"goal"`
	Input            string `json:"input"`
	Criteria         string `json:"criteria"`
	AssigneeID       string `json:"assignee_id"`
	DependsOnIndexes []int  `json:"depends_on_indexes"`
	DependsOn        []int  `json:"depends_on"`
}

func (t planTaskJSON) deps() []int {
	if len(t.DependsOnIndexes) > 0 {
		return t.DependsOnIndexes
	}
	return t.DependsOn
}
