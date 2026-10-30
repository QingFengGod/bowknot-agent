package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/egoist/mygo"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/gateway"
	"github.com/QingFengGod/bowknot-agent/internal/group"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/plugin"
)

// setUpGroup wires the run store, the run manager and the assistant planner.
// Missing data paths leave runs in memory so the assistant still works.
func (a *App) setUpGroup() {
	groupsDir, err := platform.GroupsDir()
	if err != nil {
		log.Printf("app: groups directory: %v; runs are memory only", err)
	}
	runsDir, err := platform.RunsDir()
	if err != nil {
		log.Printf("app: runs directory: %v; runs are memory only", err)
	}
	store := group.NewStore(groupsDir, runsDir)
	if err := store.Load(); err != nil {
		log.Printf("app: load runs: %v", err)
	}
	a.groupStore = store

	manager := group.NewManager(store, groupAgents{registry: a.agents, chatter: a.gateway}, a.groupChat)
	manager.Tools = groupTools{agents: a.agents, plugins: a.pluginStore}
	manager.OnRunCreated = func(group.Run) {
		mygo.RunOnMain(a.invalidateMain)
	}
	manager.OnRunUpdate = func(group.Run) {
		mygo.RunOnMain(a.invalidateMain)
	}
	manager.OnAssistantReply = func(conversationID, runID, text string) {
		mygo.RunOnMain(func() { a.appendRunSummary(conversationID, text) })
	}
	a.groupManager = manager
	if a.assistantPage != nil {
		a.assistantPage.Planner = manager
	}
}

// groupChat adapts the gateway to the non-streaming call the run manager uses.
func (a *App) groupChat(ctx context.Context, req gateway.ChatRequest) (gateway.ChatResponse, error) {
	if a.gateway == nil {
		return gateway.ChatResponse{}, errors.New("app: 网关未配置")
	}
	return a.gateway.Chat(ctx, req, agent.OriginAgent)
}

// appendRunSummary writes the final run summary into the assistant
// conversation that started it. Group conversations are not in the assistant
// store and are skipped here.
func (a *App) appendRunSummary(conversationID, text string) {
	if a.assistantPage == nil || conversationID == "" {
		return
	}
	if _, ok := a.assistantPage.Store.Get(conversationID); !ok {
		return
	}
	if _, err := a.assistantPage.Store.Append(conversationID, agent.Message{
		Role:    agent.RoleAssistant,
		Content: text,
		Status:  agent.StatusOK,
		At:      time.Now().Unix(),
	}); err != nil {
		log.Printf("app: 保存 Run 汇总: %v", err)
	}
	if err := a.assistantPage.Store.Save(conversationID); err != nil {
		log.Printf("app: 保存会话: %v", err)
	}
	a.invalidateMain()
}

// groupAgents adapts the agent registry to the run manager contract.
type groupAgents struct {
	registry *agent.Registry
	chatter  agent.Chatter
}

func (g groupAgents) Agent(id string) (group.AgentSpec, bool) {
	if g.registry == nil {
		return group.AgentSpec{}, false
	}
	a, ok := g.registry.Get(id)
	if !ok {
		return group.AgentSpec{}, false
	}
	return group.AgentSpec{
		ID:         a.ID,
		Name:       a.Name,
		ProviderID: a.ProviderID,
		Model:      a.Model,
		Enabled:    a.Enabled,
	}, true
}

func (g groupAgents) AgentIDs() []string {
	if g.registry == nil {
		return nil
	}
	list := g.registry.List()
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func (g groupAgents) Prompt(agentID, query string) (string, string) {
	if g.registry == nil {
		return "", ""
	}
	return g.registry.SystemPrompt(g.chatter, agentID, query)
}

// groupTools offers the plugin tools enabled for one agent to the run
// executor. The host denies high-risk writes because runs cannot ask the user
// for approval yet.
type groupTools struct {
	agents  *agent.Registry
	plugins *plugin.Store
}

func (g groupTools) Tools(agentID string) []group.ToolSpec {
	names := g.declared(agentID)
	if len(names) == 0 {
		return nil
	}
	schemas := plugin.ToolSchemas(names)
	out := make([]group.ToolSpec, 0, len(schemas))
	for _, schema := range schemas {
		out = append(out, group.ToolSpec{
			Name:        schema.Name,
			Description: schema.Description,
			Params:      schema.Params,
		})
	}
	return out
}

func (g groupTools) Call(ctx context.Context, agentID, name, argsJSON string) (string, error) {
	if g.agents == nil {
		return "", errors.New("app: 工具不可用")
	}
	declared := g.declared(agentID)
	found := false
	for _, declaredName := range declared {
		if declaredName == name {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%w：%s", plugin.ErrToolDisabled, name)
	}
	host := &plugin.ToolHost{
		Root:   g.agents.Workspace(agentID).Dir(),
		Memory: memorySearcher{store: g.agents.Memory(agentID)},
	}
	if g.plugins != nil {
		host.Audit = func(tool, summary string) {
			g.plugins.Record("tool", agentID, tool+" "+summary)
		}
	}
	return host.Call(ctx, name, argsJSON)
}

// declared returns the sorted tool names the enabled plugins of one agent
// declare. It is the allow list used both for the prompt and for execution.
func (g groupTools) declared(agentID string) []string {
	if g.agents == nil || g.plugins == nil {
		return nil
	}
	set := map[string]bool{}
	for _, id := range g.agents.EnabledPlugins(agentID) {
		e, ok := g.plugins.Get(id)
		if !ok {
			continue
		}
		for _, tool := range e.Tools {
			set[tool] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
