package app

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/task"
)

// setUpAgent wires the assistant module: its conversation store, the sub
// agent store, the task store, the gateway chatter and the page.
func (a *App) setUpAgent() {
	conversationDir, taskDir, subAgentsPath := agentPaths()
	page := newAgentPage(a.gateway, conversationDir, taskDir, subAgentsPath)
	page.Repaint = a.invalidateMain
	page.Dispatch = func(fn func()) { mygo.RunOnMain(fn) }
	a.agentPage = page
}

// agentPaths returns the conversation directory, the task directory and the
// sub agent file. An empty base directory keeps everything in memory for this
// run.
func agentPaths() (conversationDir, taskDir, subAgentsPath string) {
	dir, err := platform.AgentDir()
	if err != nil {
		log.Printf("app: agent directory: %v; assistant data is kept in memory", err)
		return "", "", ""
	}
	tasks, err := platform.TaskDir()
	if err != nil {
		log.Printf("app: task directory: %v; tasks are kept in memory", err)
		return conversationsDir(dir), "", filepath.Join(dir, agent.SubAgentsFileName)
	}
	return conversationsDir(dir), tasks, filepath.Join(dir, agent.SubAgentsFileName)
}

// conversationsDir returns the conversation directory of an agent directory.
// An empty agent directory keeps the conversations in memory for this run.
func conversationsDir(agentDir string) string {
	if agentDir == "" {
		return ""
	}
	return filepath.Join(agentDir, "conversations")
}

// newAgentPage opens the conversations, the sub agents and the tasks in the
// given paths, then selects the most recent conversation.
func newAgentPage(chatter agent.Chatter, conversationDir, taskDir, subAgentsPath string) *agent.Page {
	store := agent.NewStore(conversationDir)
	if err := store.Load(); err != nil {
		log.Printf("app: load conversations: %v", err)
	}
	subs := agent.NewSubAgentStore(subAgentsPath)
	if err := subs.Load(); err != nil {
		log.Printf("app: load sub agents: %v", err)
	}
	tasks := task.NewStore(taskDir)
	if err := tasks.Load(); err != nil {
		log.Printf("app: load tasks: %v", err)
	}
	page := agent.NewPage(chatter, store, tasks, subs)
	page.SelectRecent()
	return page
}
