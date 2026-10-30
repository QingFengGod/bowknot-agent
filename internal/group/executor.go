package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
)

// 子 Agent 的协作工具名。
const (
	ToolGroupPost    = "group.post"
	ToolGroupRequest = "group.request"
	ToolGroupWait    = "group.wait"
	ToolGroupHandoff = "group.handoff"
	ToolGroupBlocked = "group.blocked"
)

// 工具与协议限制。
const (
	// MaxToolContentRunes 限制一次群工具调用的正文长度，与消息上限一致。
	MaxToolContentRunes = MaxMessageRunes
	// MaxToolResultRunes 是回填给模型的单个工具结果上限。
	MaxToolResultRunes = 800
	// defaultStepTokens bounds one step of the sub agent loop.
	defaultStepTokens = 1200
	// defaultStepTemperature keeps tool decisions deterministic.
	defaultStepTemperature = 0.2
)

// ErrStepLimit 表示任务达到单任务步数上限（FR-GRP-07）。
var ErrStepLimit = fmt.Errorf("任务达到步数上限 %d", DefaultMaxTaskSteps)

// ToolSpec describes one tool offered to a sub agent.
type ToolSpec struct {
	Name        string
	Description string
	Params      string
}

// Toolbox executes the plugin tools of one agent and lists them. Group tools
// are handled by the executor itself and are not part of the toolbox.
type Toolbox interface {
	// Tools returns the plugin tools one agent may call.
	Tools(agentID string) []ToolSpec
	// Call runs one plugin tool and returns the text the model receives.
	Call(ctx context.Context, agentID, name, argsJSON string) (string, error)
}

// AgentSpec is the execution view of one agent.
type AgentSpec struct {
	ID         string
	Name       string
	ProviderID string
	Model      string
	Enabled    bool
}

// AgentProvider supplies agent specs and system prompts to the executor and
// the run manager without depending on the agent package.
type AgentProvider interface {
	// Agent returns one agent; the main agent has ID MainAgentID.
	Agent(id string) (AgentSpec, bool)
	// AgentIDs returns every configured agent, including disabled ones.
	AgentIDs() []string
	// Prompt returns the system prompt of one agent for a query, including its
	// memory and plugin sections.
	Prompt(agentID, query string) (prompt, notice string)
}

// ChatFunc is the non-streaming chat call the executor uses.
type ChatFunc func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error)

// Executor runs one task as a multi-step tool loop for its sub agent.
type Executor struct {
	Store       *Store
	Bus         *Bus
	Chat        ChatFunc
	Agents      AgentProvider
	Tools       Toolbox
	MaxStep     int
	WaitTimeout time.Duration
}

// NewExecutor returns an executor over a run store. MaxStep zero falls back to
// the Run budget.
func NewExecutor(store *Store, bus *Bus, chat ChatFunc) *Executor {
	return &Executor{Store: store, Bus: bus, Chat: chat, MaxStep: DefaultMaxTaskSteps}
}

// RunTask executes one task until the model reports, the step budget is spent
// or the context ends. Progress, requests and the final report go through the
// bus; the structured report is returned in the outcome.
func (e *Executor) RunTask(ctx context.Context, run *Run, t *Task) Outcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if e == nil || e.Bus == nil || e.Chat == nil || e.Agents == nil {
		return failure("executor", "执行器未配置", nil)
	}
	spec, ok := e.Agents.Agent(t.AssigneeID)
	if !ok || !spec.Enabled {
		return failure("model", "子 Agent 不可用，无法执行", nil)
	}
	steps := e.MaxStep
	if steps <= 0 && run != nil {
		steps = run.Budget.MaxTaskSteps
	}
	if steps <= 0 {
		steps = DefaultMaxTaskSteps
	}
	persona, notice := e.Agents.Prompt(t.AssigneeID, t.Goal)
	if notice != "" {
		log.Printf("group: 子 Agent %s 提示注入：%s", t.AssigneeID, notice)
	}
	system := persona + "\n\n" + e.rules(t)

	msgs := []gateway.ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: taskInstruction(t)},
	}
	var lastRequest string
	for step := 0; step < steps; step++ {
		if err := ctx.Err(); err != nil {
			e.blocked(t, "任务已中止")
			return failure("canceled", "任务已中止", err)
		}
		resp, err := e.Chat(ctx, gateway.ChatRequest{
			ProviderID: spec.ProviderID,
			Model:      spec.Model,
			TaskID:     t.ID,
			Messages:   msgs,
			MaxTokens:  defaultStepTokens,
		})
		if err != nil {
			if ctx.Err() != nil {
				e.blocked(t, "任务已中止")
				return failure("canceled", "任务已中止", ctx.Err())
			}
			e.blocked(t, "模型调用失败："+err.Error())
			return failure(string(gateway.KindOf(err)), err.Error(), err)
		}
		content := strings.TrimSpace(resp.Content)
		if content == "" {
			e.blocked(t, "模型没有返回内容")
			return failure("model", "模型没有返回内容", nil)
		}
		action, parseErr := parseAction(content)
		if parseErr != nil {
			// A plain answer is accepted as the final unstructured report.
			report := fallbackReport(content)
			e.report(t, report)
			return success(report, resp)
		}
		if action.Action == "final" || action.Action == "answer" {
			report, err := action.report()
			if err != nil {
				msgs = append(msgs,
					gateway.ChatMessage{Role: "assistant", Content: content},
					gateway.ChatMessage{Role: "user", Content: "汇报缺少 conclusion，请补全后重新输出。"})
				continue
			}
			e.report(t, report)
			return success(report, resp)
		}
		if action.Action != "tool" || action.Tool == "" {
			msgs = append(msgs,
				gateway.ChatMessage{Role: "assistant", Content: content},
				gateway.ChatMessage{Role: "user", Content: "无法识别的动作，请只输出工具调用或最终汇报 JSON。"})
			continue
		}

		if action.Tool == ToolGroupWait && action.ReplyTo == "" {
			action.ReplyTo = lastRequest
		}
		result, reqID, toolErr := e.callTool(ctx, t, action)
		if reqID != "" {
			lastRequest = reqID
		}
		if errors.Is(toolErr, ErrMessageLimit) {
			return failure("message_limit", "消息已达上限，协作已暂停，请用户决定继续或终止", toolErr)
		}
		if toolErr != nil {
			result = "工具失败：" + toolErr.Error()
		}
		msgs = append(msgs,
			gateway.ChatMessage{Role: "assistant", Content: content},
			gateway.ChatMessage{Role: "user", Content: clipText("工具结果："+result, MaxToolResultRunes)})
	}
	e.blocked(t, fmt.Sprintf("任务达到步数上限 %d，已暂停", steps))
	out := failure("step_limit", ErrStepLimit.Error(), ErrStepLimit)
	return out
}

// callTool 执行一个工具调用，返回给模型的文本与新产生的请求消息 ID。
func (e *Executor) callTool(ctx context.Context, t *Task, action actionJSON) (string, string, error) {
	args := action.args()
	from := t.AssigneeID
	switch action.Tool {
	case ToolGroupPost:
		content := strings.TrimSpace(args.Content)
		if content == "" {
			return "", "", errors.New("group.post 缺少 content")
		}
		if err := checkRunes("消息", content, 1, MaxMessageRunes); err != nil {
			return "", "", err
		}
		msg, err := e.Bus.Post(from, t.ID, MessageProgress, "", content)
		if err != nil && !errors.Is(err, ErrMessageWrite) {
			return "", "", err
		}
		return "进展已广播（" + msg.ID + "）" + writeNotice(err), "", nil
	case ToolGroupRequest:
		if args.To == "" {
			return "", "", errors.New("group.request 缺少 to")
		}
		content := strings.TrimSpace(args.Content)
		if content == "" {
			return "", "", errors.New("group.request 缺少 content")
		}
		msg, err := e.Bus.Request(from, t.ID, args.To, content)
		if err != nil && !errors.Is(err, ErrMessageWrite) {
			return "", "", err
		}
		return "请求已发送（" + msg.ID + "），等待答复可用 group.wait" + writeNotice(err), msg.ID, nil
	case ToolGroupWait:
		timeout := time.Duration(args.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = e.WaitTimeout
		}
		if timeout <= 0 {
			timeout = DefaultWaitTimeout
		}
		replyTo := args.ReplyTo
		if replyTo == "" && action.ReplyTo != "" {
			replyTo = action.ReplyTo
		}
		msg, err := e.Bus.WaitReply(ctx, from, replyTo, timeout)
		if err != nil {
			return "", "", err
		}
		return fmt.Sprintf("收到 %s 的答复（%s）：%s", msg.FromID, msg.ID, msg.Content), "", nil
	case ToolGroupHandoff:
		if args.To == "" {
			return "", "", errors.New("group.handoff 缺少 to")
		}
		content := strings.TrimSpace(args.Content)
		if content == "" {
			return "", "", errors.New("group.handoff 缺少 content")
		}
		msg, err := e.Bus.Post(from, t.ID, MessageHandoff, args.To, content)
		if err != nil && !errors.Is(err, ErrMessageWrite) {
			return "", "", err
		}
		return "交接已广播（" + msg.ID + "）" + writeNotice(err), "", nil
	case ToolGroupBlocked:
		content := strings.TrimSpace(args.Content)
		if content == "" {
			content = "任务遇到阻塞"
		}
		_, err := e.Bus.Post(from, t.ID, MessageBlocked, "", content)
		if err != nil && !errors.Is(err, ErrMessageWrite) {
			return "", "", err
		}
		return "阻塞已上报" + writeNotice(err), "", nil
	}
	if e.Tools == nil {
		return "", "", fmt.Errorf("未知工具：%s", action.Tool)
	}
	out, err := e.Tools.Call(ctx, from, action.Tool, action.rawArgs())
	return out, "", err
}

func writeNotice(err error) string {
	if err == nil {
		return ""
	}
	return "；消息已保留在内存，但写入消息文件失败：" + err.Error()
}

func (e *Executor) blocked(t *Task, text string) {
	if _, err := e.Bus.Post(t.AssigneeID, t.ID, MessageBlocked, "", clipText(text, MaxMessageRunes)); err != nil {
		log.Printf("group: 上报阻塞失败: %v", err)
	}
}

func (e *Executor) report(t *Task, r Report) {
	content := strings.TrimSpace(r.Conclusion)
	if content == "" {
		content = "任务已完成，但没有结论"
	}
	if _, err := e.Bus.Post(t.AssigneeID, t.ID, MessageReport, "", clipText(content, MaxMessageRunes)); err != nil {
		log.Printf("group: 保存汇报失败: %v", err)
	}
}

func (e *Executor) rules(t *Task) string {
	var b strings.Builder
	b.WriteString("你在群聊中执行一个任务。每一步只输出一个 JSON 对象，不要解释、不要 Markdown 代码块。\n")
	b.WriteString("可用动作：\n")
	b.WriteString(`- 调用工具：{"action":"tool","tool":"名称","args":{...}}` + "\n")
	b.WriteString(`- 最终汇报：{"action":"final","report":{"conclusion":"结论","artifacts":["产出物"],"open_issues":["未解决"],"summary":"过程摘要"}}` + "\n\n")
	b.WriteString("可用工具：\n")
	b.WriteString("- `group.post` 广播进展。参数：{\"content\":\"正文\"}\n")
	b.WriteString("- `group.request` 向指定成员发起请求。参数：{\"to\":\"a-xxxxxxxx\",\"content\":\"正文\"}\n")
	b.WriteString("- `group.wait` 等待答复。参数：{\"reply_to\":\"请求消息 ID，可省略\",\"timeout_seconds\":120}\n")
	b.WriteString("- `group.handoff` 交接产物。参数：{\"to\":\"a-xxxxxxxx\",\"content\":\"正文\"}\n")
	b.WriteString("- `group.blocked` 上报阻塞。参数：{\"content\":\"正文\"}\n")
	if e.Tools != nil {
		for _, spec := range e.Tools.Tools(t.AssigneeID) {
			b.WriteString(fmt.Sprintf("- `%s`：%s 参数：%s\n", spec.Name, spec.Description, spec.Params))
		}
	}
	b.WriteString("\n只能在任务范围内协作：不得改变目标、创建任务或取消他人的任务；跨 Run 通信被禁止。")
	return b.String()
}

func taskInstruction(t *Task) string {
	var b strings.Builder
	b.WriteString("目标：")
	b.WriteString(t.Goal)
	if strings.TrimSpace(t.Input) != "" {
		b.WriteString("\n\n输入：\n")
		b.WriteString(t.Input)
	}
	if strings.TrimSpace(t.Criteria) != "" {
		b.WriteString("\n\n完成标准：")
		b.WriteString(t.Criteria)
	}
	return b.String()
}

// actionJSON is one step of the sub agent tool protocol, and also carries the
// delegate plan emitted by the main agent.
type actionJSON struct {
	Action     string          `json:"action"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args"`
	Content    string          `json:"content"`
	Report     *reportJSON     `json:"report"`
	ReplyTo    string          `json:"reply_to,omitempty"`
	Conclusion string          `json:"conclusion,omitempty"`
	Artifacts  []string        `json:"artifacts,omitempty"`
	OpenIssues []string        `json:"open_issues,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Tasks      []planTaskJSON  `json:"tasks,omitempty"`
	PlanBody   *planBodyJSON   `json:"plan,omitempty"`
}

type reportJSON struct {
	Conclusion   string   `json:"conclusion"`
	Artifacts    []string `json:"artifacts"`
	OpenIssues   []string `json:"open_issues"`
	Summary      string   `json:"summary"`
	Unstructured bool     `json:"unstructured,omitempty"`
}

// argsJSON 是工具参数。
type argsJSON struct {
	To             string `json:"to"`
	Content        string `json:"content"`
	ReplyTo        string `json:"reply_to"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (a actionJSON) args() argsJSON {
	var out argsJSON
	if len(a.Args) > 0 {
		_ = json.Unmarshal(a.Args, &out)
	}
	if out.Content == "" {
		out.Content = a.Content
	}
	if out.ReplyTo == "" {
		out.ReplyTo = a.ReplyTo
	}
	return out
}

func (a actionJSON) rawArgs() string {
	if len(a.Args) == 0 {
		return "{}"
	}
	return string(a.Args)
}

func (a actionJSON) report() (Report, error) {
	body := a.Report
	if body == nil {
		if strings.TrimSpace(a.Conclusion) == "" {
			return Report{}, errors.New("conclusion 为空")
		}
		body = &reportJSON{
			Conclusion: a.Conclusion,
			Artifacts:  a.Artifacts,
			OpenIssues: a.OpenIssues,
			Summary:    a.Summary,
		}
	}
	if strings.TrimSpace(body.Conclusion) == "" {
		return Report{}, errors.New("conclusion 为空")
	}
	return Report{
		Conclusion:   strings.TrimSpace(body.Conclusion),
		Artifacts:    body.Artifacts,
		OpenIssues:   body.OpenIssues,
		Summary:      body.Summary,
		Unstructured: body.Unstructured,
	}, nil
}

func parseAction(content string) (actionJSON, error) {
	body := stripJSONFence(content)
	var a actionJSON
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		return actionJSON{}, err
	}
	return a, nil
}

func (a actionJSON) tasks() []planTaskJSON {
	if len(a.Tasks) > 0 {
		return a.Tasks
	}
	if a.PlanBody != nil {
		return a.PlanBody.Tasks
	}
	return nil
}

func failure(code, message string, err error) Outcome {
	return Outcome{Err: err, ErrorCode: code, ErrorMessage: message}
}

func success(r Report, resp gateway.ChatResponse) Outcome {
	return Outcome{
		Report:         &r,
		InputTokens:    resp.InputTokens,
		OutputTokens:   resp.OutputTokens,
		UsageEstimated: !resp.HasUsage,
		DurationMS:     resp.Duration.Milliseconds(),
	}
}

func fallbackReport(content string) Report {
	return Report{Conclusion: clipText(content, 4000), OpenIssues: []string{"未按结构返回"}, Unstructured: true}
}

func clipText(text string, n int) string {
	return truncateRunes(text, n)
}

func stripJSONFence(content string) string {
	body := strings.TrimSpace(content)
	if !strings.HasPrefix(body, "```") {
		return body
	}
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	body = strings.TrimSpace(body)
	body = strings.TrimSuffix(body, "```")
	return strings.TrimSpace(body)
}
