package app

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/shell"
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

	// The task center shares the task store with the assistant page, so a
	// plan confirmed in one place appears in the other without a reload.
	taskPage := task.NewPage(page.Tasks, taskActions{page: page})
	taskPage.Titles = func(conversationID string) string {
		if conv, ok := page.Store.Get(conversationID); ok {
			return conv.TitleText()
		}
		return ""
	}
	taskPage.OpenConversation = a.openConversation
	taskPage.Repaint = a.invalidateMain
	a.taskPage = taskPage
	// Background failures belong to both pages: the conversation shows them
	// with the task card and the task center shows them in the detail.
	page.Dispatcher.Notice = func(text string) {
		page.ActionError = text
		taskPage.ActionError = text
		a.invalidateMain()
	}
}

// taskActions adapts the assistant dispatcher to the task center contract.
type taskActions struct{ page *agent.Page }

func (t taskActions) Confirm(groupID string) error { return t.page.Dispatcher.Confirm(groupID) }

func (t taskActions) Cancel(groupID string) { t.page.Dispatcher.Cancel(groupID) }

func (t taskActions) Retry(groupID, taskID string) error {
	return t.page.Dispatcher.Retry(groupID, taskID)
}

func (t taskActions) Resume(groupID string) error { return t.page.Dispatcher.Resume(groupID) }

func (t taskActions) Terminate(groupID string) error { return t.page.Dispatcher.Terminate(groupID) }

func (t taskActions) Delete(groupID string) error { return t.page.Dispatcher.Delete(groupID) }

func (t taskActions) CleanTerminal() (int, error) { return t.page.Dispatcher.CleanTerminal() }

func (t taskActions) Busy() bool { return t.page.Dispatcher.Busy() }

// openConversation switches to the assistant module and selects the
// conversation a task plan was started from.
func (a *App) openConversation(conversationID string) {
	if a.agentPage == nil {
		return
	}
	a.agentPage.SelectConversation(conversationID)
	a.shell.Current = shell.ModuleAgent
	a.invalidateMain()
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
