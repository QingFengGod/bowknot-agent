package app

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
)

// setUpAgent wires the assistant module: its conversation store, the gateway
// chatter and the page.
func (a *App) setUpAgent() {
	dir, err := platform.AgentDir()
	if err != nil {
		log.Printf("app: agent directory: %v; conversations are kept in memory", err)
	}
	page := newAgentPage(a.gateway, conversationsDir(dir))
	page.Repaint = a.invalidateMain
	page.Dispatch = func(fn func()) { mygo.RunOnMain(fn) }
	a.agentPage = page
}

// conversationsDir returns the conversation directory of an agent directory.
// An empty agent directory keeps the conversations in memory for this run.
func conversationsDir(agentDir string) string {
	if agentDir == "" {
		return ""
	}
	return filepath.Join(agentDir, "conversations")
}

// newAgentPage opens the conversations in dir and selects the most recent one.
func newAgentPage(chatter agent.Chatter, dir string) *agent.Page {
	store := agent.NewStore(dir)
	if err := store.Load(); err != nil {
		log.Printf("app: load conversations: %v", err)
	}
	page := agent.NewPage(chatter, store)
	page.SelectRecent()
	return page
}
