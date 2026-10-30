package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/memory"
)

func TestSystemPromptRunsDateCheckForSubAgent(t *testing.T) {
	f := &fakeChatter{snapshot: onlineSnapshot("m1")}
	agents := NewRegistry(t.TempDir())
	a, err := agents.Create(Agent{Name: "子 Agent", Enabled: true, ProviderID: "p-00000001", Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Workspace(a.ID).Save(PersonaFileName, "只做调研"); err != nil {
		t.Fatal(err)
	}
	prompt, notice := agents.SystemPrompt(f, a.ID, "调研")
	if notice != "" {
		t.Errorf("notice = %q", notice)
	}
	if !strings.Contains(prompt, "只做调研") {
		t.Errorf("系统提示缺少人设: %s", prompt)
	}
	if weeks := agents.Memory(a.ID).Weeks(); len(weeks) != 1 {
		t.Fatalf("系统提示未执行记忆日期检查: %+v", weeks)
	}
	if prompt, _ := agents.SystemPrompt(f, "a-00000000", "调研"); prompt != "" {
		t.Errorf("未知 Agent 的系统提示 = %q", prompt)
	}
}

// newMemoryAgent creates a sub agent bound to the fake target and opens it.
func newMemoryAgent(t *testing.T, p *AgentPage, agents *Registry) Agent {
	t.Helper()
	a, err := agents.Create(Agent{
		Name: "记忆测试", Enabled: true,
		ProviderID: "p-00000001", Model: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agents.Workspace(a.ID).Save(PersonaFileName, "帮忙记事"); err != nil {
		t.Fatal(err)
	}
	p.SelectAgent(a.ID)
	return a
}

func TestSendRunsDateCheckAndInjectsMemory(t *testing.T) {
	f := &fakeChatter{snapshot: onlineSnapshot("m1")}
	p, _, agents := newTestAgentPage(t, f)
	a := newMemoryAgent(t, p, agents)
	p.Input = "你好"
	p.Send()
	waitFor(t, func() bool { return !p.Runner.Busy() })

	weeks := agents.Memory(a.ID).Weeks()
	if len(weeks) != 1 {
		t.Fatalf("对话开始未创建当周文件: %+v", weeks)
	}
	start, _ := memory.WeekRange(time.Now())
	if weeks[0].Start != start.Format("2006-01-02") {
		t.Fatalf("当周文件 = %+v", weeks[0])
	}
	req := f.lastRequest()
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("请求缺少 system 提示: %+v", req.Messages)
	}
	system := req.Messages[0].Content
	for _, want := range []string{"帮忙记事", "长期记忆", "本周记录进行中"} {
		if !strings.Contains(system, want) {
			t.Errorf("系统提示缺少 %q: %s", want, system)
		}
	}
}

func TestSendSummarizesPastWeekInBackground(t *testing.T) {
	past := memory.WeekFileFor(time.Now().AddDate(0, 0, -7))
	f := &fakeChatter{
		snapshot: onlineSnapshot("m1"),
		handler: func(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
			if strings.Contains(req.Messages[0].Content, "记忆摘要") {
				return gateway.ChatResponse{Content: "用户偏好深色主题。"}, nil
			}
			return gateway.ChatResponse{Content: "回复"}, nil
		},
	}
	p, _, agents := newTestAgentPage(t, f)
	a := newMemoryAgent(t, p, agents)
	mem := agents.Memory(a.ID)
	if err := mem.SaveWeek(past, "用户希望默认深色主题。\n"); err != nil {
		t.Fatal(err)
	}

	p.Input = "继续"
	p.Send()
	waitFor(t, func() bool { return !p.Runner.Busy() })
	waitFor(t, func() bool {
		for _, w := range mem.Weeks() {
			if w.File == past && w.Status == memory.SummaryReady && w.Summary != "" {
				return true
			}
		}
		return false
	})

	section := mem.Context("", 800)
	if !strings.Contains(section, "用户偏好深色主题。") {
		t.Fatalf("摘要未写入索引: %s", section)
	}
	// The next message injects the summary and a retrieved snippet.
	p.Input = "深色主题"
	p.Send()
	waitFor(t, func() bool { return !p.Runner.Busy() })
	system := f.lastRequest().Messages[0].Content
	for _, want := range []string{"用户偏好深色主题。", "相关记忆片段"} {
		if !strings.Contains(system, want) {
			t.Errorf("系统提示缺少 %q: %s", want, system)
		}
	}
}

func TestMemoryModalManagesWeeks(t *testing.T) {
	f := &fakeChatter{snapshot: onlineSnapshot("m1")}
	p, _, agents := newTestAgentPage(t, f)
	a := newMemoryAgent(t, p, agents)
	tester := newAgentPageTester(t, p)
	tester.Frame()
	clickAndFrame(t, tester, "记忆")
	if !p.MemoryOpen {
		t.Fatal("记忆按钮未打开面板")
	}
	if !tester.HasText("长期记忆 · 记忆测试") || !tester.HasText("清除记忆") {
		t.Fatal("记忆面板内容缺失")
	}
	weeks := p.memoryWeeks()
	if len(weeks) != 1 || p.MemoryWeek != weeks[0].File {
		t.Fatalf("面板未选中当周: %+v / %q", weeks, p.MemoryWeek)
	}
	if !tester.HasText(weeks[0].Start + " ~ " + weeks[0].End) {
		t.Fatal("周列表未显示当周")
	}

	p.MemoryDraft = "# 本周\n\n记住：导出前先确认。\n"
	p.SaveMemoryWeek()
	if p.MemoryError != "" {
		t.Fatalf("保存失败: %s", p.MemoryError)
	}
	content, err := agents.Memory(a.ID).ReadWeek(p.MemoryWeek)
	if err != nil || !strings.Contains(content, "导出前先确认") {
		t.Fatalf("保存内容 = %q, %v", content, err)
	}

	p.MemorySearch = "导出"
	p.SearchMemory()
	if len(p.MemoryResults) == 0 || !strings.Contains(p.MemoryResults[0].Snippet, "导出") {
		t.Fatalf("搜索无命中: %+v", p.MemoryResults)
	}

	p.ExportMemory()
	if p.MemoryError != "" {
		t.Fatalf("导出失败: %s", p.MemoryError)
	}
	exports, err := filepath.Glob(filepath.Join(agents.Workspace(a.ID).Dir(), memoryExportPrefix+"*.md"))
	if err != nil || len(exports) != 1 {
		t.Fatalf("导出文件 = %v, %v", exports, err)
	}
	exported, err := os.ReadFile(exports[0])
	if err != nil || !strings.Contains(string(exported), "导出前先确认") {
		t.Fatalf("导出内容 = %q, %v", exported, err)
	}

	p.RequestClearMemory()
	p.ClearMemory()
	if weeks := p.memoryWeeks(); len(weeks) != 0 {
		t.Fatalf("清除后仍有记忆: %+v", weeks)
	}
	if p.MemoryWeek != "" || p.MemoryDraft != "" {
		t.Fatalf("清除后编辑器未重置: %q %q", p.MemoryWeek, p.MemoryDraft)
	}

	p.CloseMemory()
	if p.MemoryOpen {
		t.Fatal("关闭按钮未生效")
	}
}
