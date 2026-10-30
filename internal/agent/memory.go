package agent

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	mygoui "github.com/egoist/mygo/ui"

	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/memory"
	"github.com/QingFengGod/bowknot-agent/internal/ui"
)

const (
	// memoryBudgetRunes is the rune budget of the memory section injected into
	// the system prompt.
	memoryBudgetRunes = 800
	// memorySummaryTokens bounds one summary reply.
	memorySummaryTokens = 600
	// memorySummaryTimeout bounds one background summary run.
	memorySummaryTimeout = 30 * time.Second
	// memoryListH is the height of the week list in the memory modal.
	memoryListH = 140.0
	// memoryExportPrefix names the exported file inside the workspace.
	memoryExportPrefix = "memory-export-"
)

// memoryChat returns the summary call over the shared chatter. A nil chatter or
// an unpinned target leaves summary generation disabled.
func memoryChat(chatter Chatter, providerID, model string) memory.ChatFunc {
	if chatter == nil || providerID == "" || model == "" {
		return nil
	}
	temperature := 0.2
	return func(ctx context.Context, system, user string) (string, error) {
		resp, err := chatter.Chat(ctx, gateway.ChatRequest{
			ProviderID: providerID,
			Model:      model,
			Messages: []gateway.ChatMessage{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
			},
			MaxTokens:   memorySummaryTokens,
			Temperature: &temperature,
		}, OriginAgent)
		if err != nil {
			return "", err
		}
		return resp.Content, nil
	}
}

// memoryEmbed returns the semantic retrieval backend. A gateway without an
// embedding route reports an error and retrieval stays on the keyword path.
func memoryEmbed(chatter Chatter) memory.EmbedFunc {
	if chatter == nil {
		return nil
	}
	return func(ctx context.Context, texts []string) ([][]float32, error) {
		resp, err := chatter.Embed(ctx, gateway.EmbedRequest{Input: texts}, OriginAgent)
		if err != nil {
			return nil, err
		}
		if len(resp.Vectors) != len(texts) {
			return nil, errors.New("向量数量与文本数量不一致")
		}
		return resp.Vectors, nil
	}
}

// agentMemoryPrompt runs the date check and assembles the system prompt of one
// agent: the workspace text first, then the memory section, then the skill
// section of the enabled plugins. Pending summaries are generated in the
// background so a reply never waits for them. The notice reports plugins that
// failed injection and were removed from the enable list.
func agentMemoryPrompt(agents *Registry, chatter Chatter, agentID, providerID, model, query string) (prompt, notice string) {
	prompt = agentPrompt(agents, agentID)
	if agents == nil {
		return prompt, ""
	}
	mem := agents.Memory(agentID)
	mem.SetEmbed(memoryEmbed(chatter))
	rep, err := mem.Ensure()
	if err != nil {
		log.Printf("agent: 检查记忆日期: %v", err)
	}
	if rep.Warning != "" {
		log.Printf("agent: %s", rep.Warning)
	}
	if len(rep.Pending) > 0 {
		if chat := memoryChat(chatter, providerID, model); chat != nil {
			go summarizeMemory(mem, chat)
		}
	}
	if section := mem.Context(query, memoryBudgetRunes); section != "" {
		prompt += "\n\n" + section
	}
	if agents.Skills != nil {
		skills, skillNotice := agents.Skills.SkillSection(agentID)
		if skills != "" {
			prompt += "\n\n" + skills
		}
		notice = skillNotice
	}
	return prompt, notice
}

// SystemPrompt returns the assembled prompt of one agent for a query, using
// that agent's own route target for the memory summary. The run manager uses
// it so sub agents keep their memory and plugin sections.
func (r *Registry) SystemPrompt(chatter Chatter, agentID, query string) (prompt, notice string) {
	if r == nil {
		return SystemPrompt, ""
	}
	a, ok := r.Get(agentID)
	if !ok {
		return "", ""
	}
	return agentMemoryPrompt(r, chatter, agentID, a.ProviderID, a.Model, query)
}

// summarizeMemory generates the pending summaries off the UI thread.
func summarizeMemory(mem *memory.Store, chat memory.ChatFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), memorySummaryTimeout)
	defer cancel()
	if _, err := mem.Summarize(ctx, chat); err != nil {
		log.Printf("agent: 生成周摘要: %v", err)
	}
}

// lastUserContent returns the latest user message of a conversation, which
// becomes the retrieval query when a reply starts.
func lastUserContent(c Conversation) string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return c.Messages[i].Content
		}
	}
	return ""
}

// memoryWeeks returns the weekly entries of the open sub agent, oldest first.
func (p *AgentPage) memoryWeeks() []memory.Week {
	if p.Agents == nil || p.CurrentAgent == "" {
		return nil
	}
	return p.Agents.Memory(p.CurrentAgent).Weeks()
}

// OpenMemory opens the memory panel of the open sub agent and refreshes the
// week list through the date check.
func (p *AgentPage) OpenMemory() {
	if p.CurrentAgent == "" {
		return
	}
	p.MemoryOpen = true
	p.MemoryError = ""
	p.MemoryNotice = ""
	p.MemorySearch = ""
	p.MemoryResults = nil
	p.ConfirmMemoryClear = false
	p.ConfirmWeekDelete = ""
	p.refreshMemory()
	weeks := p.memoryWeeks()
	if len(weeks) == 0 {
		p.MemoryWeek = ""
		p.MemoryDraft = ""
		return
	}
	p.MemoryWeek = weeks[len(weeks)-1].File
	if content, err := p.Agents.Memory(p.CurrentAgent).ReadWeek(p.MemoryWeek); err == nil {
		p.MemoryDraft = content
	}
	p.repaint()
}

// resetMemoryPanel drops the memory panel state, which a change of the open
// agent or a return to the card list needs.
func (p *AgentPage) resetMemoryPanel() {
	p.MemoryOpen = false
	p.MemoryWeek = ""
	p.MemoryDraft = ""
	p.MemorySearch = ""
	p.MemoryResults = nil
	p.MemoryError = ""
	p.MemoryNotice = ""
	p.ConfirmMemoryClear = false
	p.ConfirmWeekDelete = ""
}

// CloseMemory closes the panel without saving the draft.
func (p *AgentPage) CloseMemory() {
	p.MemoryOpen = false
	p.MemoryWeek = ""
	p.MemoryDraft = ""
	p.MemorySearch = ""
	p.MemoryResults = nil
	p.MemoryError = ""
	p.MemoryNotice = ""
	p.ConfirmMemoryClear = false
	p.ConfirmWeekDelete = ""
	p.repaint()
}

// refreshMemory runs the date check, which creates the current week file.
func (p *AgentPage) refreshMemory() {
	if p.Agents == nil || p.CurrentAgent == "" {
		return
	}
	rep, err := p.Agents.Memory(p.CurrentAgent).Ensure()
	if err != nil {
		p.MemoryError = err.Error()
		return
	}
	if rep.Warning != "" {
		p.MemoryNotice = rep.Warning
	}
}

// SelectMemoryWeek loads one week file into the editor.
func (p *AgentPage) SelectMemoryWeek(file string) {
	p.MemoryError = ""
	p.ConfirmWeekDelete = ""
	content, err := p.Agents.Memory(p.CurrentAgent).ReadWeek(file)
	if err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	p.MemoryWeek = file
	p.MemoryDraft = content
	p.repaint()
}

// SaveMemoryWeek writes the edited week file. Editing a past week marks it
// pending, so its summary is regenerated at the next conversation start.
func (p *AgentPage) SaveMemoryWeek() {
	if p.MemoryWeek == "" {
		return
	}
	if err := p.Agents.Memory(p.CurrentAgent).SaveWeek(p.MemoryWeek, p.MemoryDraft); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	p.MemoryError = ""
	p.MemoryNotice = "已保存"
	if content, err := p.Agents.Memory(p.CurrentAgent).ReadWeek(p.MemoryWeek); err == nil {
		p.MemoryDraft = content
	}
	p.repaint()
}

// RequestDeleteMemoryWeek asks for the deletion confirmation of one week.
func (p *AgentPage) RequestDeleteMemoryWeek(file string) {
	p.ConfirmWeekDelete = file
	p.repaint()
}

// DeleteMemoryWeek removes the week awaiting confirmation.
func (p *AgentPage) DeleteMemoryWeek() {
	file := p.ConfirmWeekDelete
	p.ConfirmWeekDelete = ""
	if file == "" {
		p.repaint()
		return
	}
	if err := p.Agents.Memory(p.CurrentAgent).DeleteWeek(file); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	p.MemoryWeek = ""
	p.MemoryDraft = ""
	p.MemoryNotice = "已删除 " + file
	p.repaint()
}

// RequestClearMemory asks for the confirmation of clearing every memory.
func (p *AgentPage) RequestClearMemory() {
	p.ConfirmMemoryClear = true
	p.repaint()
}

// ClearMemory removes every week file and the index of the open sub agent.
func (p *AgentPage) ClearMemory() {
	p.ConfirmMemoryClear = false
	if err := p.Agents.Memory(p.CurrentAgent).Clear(); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	p.MemoryWeek = ""
	p.MemoryDraft = ""
	p.MemoryNotice = "已清除全部记忆"
	p.repaint()
}

// SearchMemory retrieves snippets of the open sub agent. Results come from the
// semantic route when the gateway supports embeddings, and from keywords
// otherwise.
func (p *AgentPage) SearchMemory() {
	query := strings.TrimSpace(p.MemorySearch)
	if query == "" {
		p.MemoryResults = nil
		p.repaint()
		return
	}
	mem := p.Agents.Memory(p.CurrentAgent)
	mem.SetEmbed(memoryEmbed(p.Chatter))
	hits := mem.Search(context.Background(), query, 5)
	if len(hits) == 0 {
		p.MemoryNotice = "没有匹配的记忆"
	} else {
		p.MemoryNotice = ""
	}
	p.MemoryResults = hits
	p.repaint()
}

// ExportMemory writes every week of the open sub agent into a markdown file
// inside its workspace and reports the path.
func (p *AgentPage) ExportMemory() {
	mem := p.Agents.Memory(p.CurrentAgent)
	var buf bytes.Buffer
	if err := mem.Export(&buf); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	ws := p.Agents.Workspace(p.CurrentAgent)
	if err := os.MkdirAll(ws.Dir(), storeDirMode); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	path := filepath.Join(ws.Dir(), memoryExportPrefix+time.Now().Format("20060102-150405")+".md")
	if err := os.WriteFile(path, buf.Bytes(), storeFileMode); err != nil {
		p.MemoryError = err.Error()
		p.repaint()
		return
	}
	p.MemoryNotice = "已导出到 " + path
	p.repaint()
}

// memoryModal draws the memory panel of the open sub agent.
func (p *AgentPage) memoryModal(c *mygoui.Context, t *mygoui.Theme) {
	if !p.MemoryOpen {
		return
	}
	a, _ := p.Agents.Get(p.CurrentAgent)
	mygoui.Modal(c, &p.MemoryOpen, func() {
		mygoui.Text(c, "长期记忆 · "+a.Name).FontSize(15).Bold().TextColor(t.Text).Shrink(0)
		mygoui.Text(c, "按周保存在该 Agent 工作区。编辑上一周后，下次对话开始时会重新生成摘要；摘要失败时保留原文，并在下次重试。").
			FontSize(10).TextColor(t.TextMuted).Wrap()

		search := mygoui.Row(c).FillWidth().Gap(6).AlignItems(mygoui.Center)
		search.Children(func() {
			mygoui.TextInput(c, &p.MemorySearch).FillWidth().Placeholder("搜索记忆内容")
			drawButton(c, t, "搜索", false, false, p.SearchMemory)
		})
		for _, hit := range p.MemoryResults {
			hitButton := mygoui.ButtonBase(c).FillWidth().Shrink(0).Padding(4, 6).Radius(t.Radius).
				AlignItems(mygoui.Start).Justify(mygoui.Start)
			hitButton.Background(t.Surface)
			hitButton.Children(func() {
				mygoui.Column(c).FillWidth().Gap(1).Children(func() {
					mygoui.Text(c, hit.File).FontSize(10).TextColor(t.TextMuted).SingleLine()
					mygoui.Text(c, hit.Snippet).FontSize(11).TextColor(t.Text).Wrap().MaxLines(2)
				})
			})
			if hitButton.Clicked() {
				p.SelectMemoryWeek(hit.File)
			}
		}

		weeks := p.memoryWeeks()
		if len(weeks) == 0 {
			mygoui.Text(c, "还没有记忆文件").FontSize(11).TextColor(t.TextMuted).Wrap()
		} else {
			scroll := mygoui.Scroll(c).FillWidth().Height(memoryListH)
			scroll.Children(func() {
				col := mygoui.Column(c).FillWidth().Gap(2)
				col.Children(func() {
					for i := len(weeks) - 1; i >= 0; i-- {
						p.memoryWeekRow(c, t, weeks[i])
					}
				})
			})
		}

		if p.MemoryWeek != "" {
			mygoui.Text(c, "编辑 · "+p.MemoryWeek).FontSize(10).TextColor(t.TextMuted).Shrink(0)
			mygoui.TextArea(c, &p.MemoryDraft).FillWidth().Lines(6, 14)
			actions := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			actions.Children(func() {
				drawButton(c, t, "保存", true, false, p.SaveMemoryWeek)
				drawButton(c, t, "删除本周", false, false, func() { p.RequestDeleteMemoryWeek(p.MemoryWeek) })
			})
		}
		if p.ConfirmWeekDelete != "" {
			confirm := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			confirm.Children(func() {
				mygoui.Text(c, "确认删除 "+p.ConfirmWeekDelete+"？").
					FontSize(11).TextColor(ui.DangerColor(modeOf(t))).Wrap().Grow(1)
				drawButton(c, t, "取消", false, false, func() {
					p.ConfirmWeekDelete = ""
					p.repaint()
				})
				drawButton(c, t, "确认删除", true, false, p.DeleteMemoryWeek)
			})
		}
		if p.ConfirmMemoryClear {
			confirm := mygoui.Row(c).FillWidth().Gap(8).AlignItems(mygoui.Center)
			confirm.Children(func() {
				mygoui.Text(c, "清除后不可恢复，确认清除该 Agent 的全部记忆？").
					FontSize(11).TextColor(ui.DangerColor(modeOf(t))).Wrap().Grow(1)
				drawButton(c, t, "取消", false, false, func() {
					p.ConfirmMemoryClear = false
					p.repaint()
				})
				drawButton(c, t, "确认清除", true, false, p.ClearMemory)
			})
		}
		if p.MemoryError != "" {
			mygoui.Text(c, p.MemoryError).FontSize(10).
				TextColor(ui.DangerColor(modeOf(t))).Wrap()
		}
		if p.MemoryNotice != "" {
			mygoui.Text(c, p.MemoryNotice).FontSize(10).TextColor(t.TextMuted).Wrap()
		}
		footer := mygoui.Row(c).FillWidth().Gap(8).Justify(mygoui.End)
		footer.Children(func() {
			drawButton(c, t, "导出", false, false, p.ExportMemory)
			drawButton(c, t, "清除记忆", false, false, p.RequestClearMemory)
			drawButton(c, t, "关闭", false, false, p.CloseMemory)
		})
	})
	if !p.MemoryOpen {
		p.MemoryWeek = ""
		p.MemoryDraft = ""
		p.MemoryResults = nil
	}
}

// memoryWeekRow draws one week in the memory list with its summary state.
func (p *AgentPage) memoryWeekRow(c *mygoui.Context, t *mygoui.Theme, wk memory.Week) {
	selected := wk.File == p.MemoryWeek
	status := "待摘要"
	switch {
	case wk.IsCurrent(time.Now()):
		status = "本周"
	case wk.Status != memory.SummaryPending && strings.TrimSpace(wk.Summary) != "":
		status = "已摘要"
	case wk.Status != memory.SummaryPending:
		status = "空周"
	}
	b := mygoui.ButtonBase(c).FillWidth().Shrink(0).Padding(3, 6).Radius(t.Radius).
		AlignItems(mygoui.Center).Justify(mygoui.Start)
	if selected {
		b.Background(ui.AccentTint())
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		mygoui.Text(c, wk.Start+" ~ "+wk.End).FontSize(11).TextColor(t.Text).Grow(1).SingleLine()
		mygoui.Text(c, status).FontSize(9).TextColor(t.TextMuted).Shrink(0)
	})
	if b.Clicked() {
		p.SelectMemoryWeek(wk.File)
	}
}
