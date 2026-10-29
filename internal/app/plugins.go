package app

import (
	"context"
	"log"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/memory"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/plugin"
)

// setUpPlugins wires the plugin module: the installed plugin registry, the
// skill injection into the agent system prompts and the management page. A
// missing data path leaves the page idle with an empty registry.
func (a *App) setUpPlugins() {
	dir, err := platform.PluginsDir()
	if err != nil {
		log.Printf("app: plugins directory: %v; plugin management is disabled", err)
		dir = ""
	}
	store := plugin.NewStore(dir, ShellVersion)
	if err := store.Load(); err != nil {
		log.Printf("app: load plugin registry: %v", err)
	}
	a.pluginStore = store

	adapter := &pluginAgents{agents: a.agents, store: store}
	if a.agents != nil {
		a.agents.Skills = adapter
	}
	page := plugin.NewPage(store, adapter)
	page.Repaint = a.invalidateMain
	a.pluginPage = page
}

// pluginAgents adapts the agent registry and the plugin store to the plugin
// page contract and to the skill injection of the agent module. Enable and
// disable actions are recorded into the plugin audit log (FR-PLG-09).
type pluginAgents struct {
	agents *agent.Registry
	store  *plugin.Store
}

// Views returns every agent, main agent first.
func (p *pluginAgents) Views() []plugin.AgentView {
	if p.agents == nil {
		return nil
	}
	views := make([]plugin.AgentView, 0)
	for _, a := range p.agents.List() {
		views = append(views, plugin.AgentView{ID: a.ID, Name: a.Name, Main: a.IsMain()})
	}
	return views
}

func (p *pluginAgents) EnabledPlugins(agentID string) []string {
	if p.agents == nil {
		return nil
	}
	return p.agents.EnabledPlugins(agentID)
}

// SetEnabledPlugins persists the new list and records every change.
func (p *pluginAgents) SetEnabledPlugins(agentID string, ids []string) error {
	if p.agents == nil {
		return nil
	}
	previous := map[string]bool{}
	for _, id := range p.agents.EnabledPlugins(agentID) {
		previous[id] = true
	}
	if err := p.agents.SetEnabledPlugins(agentID, ids); err != nil {
		return err
	}
	next := map[string]bool{}
	for _, id := range ids {
		next[id] = true
	}
	name := agentID
	if a, ok := p.agents.Get(agentID); ok {
		name = a.Name
	}
	for _, id := range ids {
		if !previous[id] {
			p.store.Record("enable", id, "Agent "+name+" 启用")
		}
	}
	for id := range previous {
		if !next[id] {
			p.store.Record("disable", id, "Agent "+name+" 停用")
		}
	}
	return nil
}

// SkillSection implements agent.SkillProvider: the section of the enabled
// plugins of one agent, with the enable list pruned when a plugin fails
// validation.
func (p *pluginAgents) SkillSection(agentID string) (string, string) {
	ids := p.EnabledPlugins(agentID)
	section, notice, invalid := p.store.SkillSection(ids)
	if len(invalid) > 0 {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			dropped := false
			for _, bad := range invalid {
				if id == bad {
					dropped = true
					break
				}
			}
			if !dropped {
				kept = append(kept, id)
			}
		}
		if err := p.SetEnabledPlugins(agentID, kept); err != nil {
			log.Printf("app: prune invalid plugins of %s: %v", agentID, err)
		}
	}
	return section, notice
}

// ToolHost returns the built-in fs and memory tool host of one agent, bound to
// its workspace and memory. The conversation loop will call it once tool calls
// are dispatched; until then no code path reaches it. fs.write stays denied
// until the approval flow of FR-AGT-06 exists.
func (p *pluginAgents) ToolHost(agentID string) *plugin.ToolHost {
	if p.agents == nil {
		return nil
	}
	ws := p.agents.Workspace(agentID)
	return &plugin.ToolHost{
		Root:   ws.Dir(),
		Memory: memorySearcher{store: p.agents.Memory(agentID)},
		Audit: func(tool, summary string) {
			p.store.Record("tool", agentID, tool+" "+summary)
		},
	}
}

// memorySearcher adapts one agent memory store to the plugin tool contract.
type memorySearcher struct {
	store *memory.Store
}

func (m memorySearcher) SearchMemory(ctx context.Context, query string, limit int) ([]plugin.MemoryHit, error) {
	hits := m.store.Search(ctx, query, limit)
	out := make([]plugin.MemoryHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, plugin.MemoryHit{File: hit.File, Snippet: hit.Snippet})
	}
	return out, nil
}
