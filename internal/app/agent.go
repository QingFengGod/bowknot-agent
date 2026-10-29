package app

import (
	"log"

	"github.com/egoist/mygo"

	"github.com/QingFengGod/bowknot-agent/internal/agent"
	"github.com/QingFengGod/bowknot-agent/internal/platform"
	"github.com/QingFengGod/bowknot-agent/internal/shell"
	"github.com/QingFengGod/bowknot-agent/internal/task"
)

// setUpAgent wires the assistant and agent modules over the shared agent
// workspace root, conversation store, task store and runner.
func (a *App) setUpAgent() {
	agentsDir, taskDir := agentPaths()
	assistant, agentPage, agents := newAgentPages(a.gateway, agentsDir, taskDir)
	a.agents = agents
	assistant.Repaint = a.invalidateMain
	assistant.Dispatch = func(fn func()) { mygo.RunOnMain(fn) }
	agentPage.Repaint = a.invalidateMain
	agentPage.Dispatch = func(fn func()) { mygo.RunOnMain(fn) }
	agentPage.Dispatcher = assistant.Dispatcher
	a.assistantPage = assistant
	a.agentPage = agentPage

	// The task center shares the task store with the assistant page, so a
	// plan confirmed in one place appears in the other without a reload.
	taskPage := task.NewPage(assistant.Tasks, taskActions{page: assistant})
	taskPage.Titles = func(conversationID string) string {
		if conv, ok := assistant.Store.Get(conversationID); ok {
			return conv.TitleText()
		}
		return ""
	}
	taskPage.OpenConversation = a.openConversation
	taskPage.Repaint = a.invalidateMain
	a.taskPage = taskPage
	// Background failures belong to both pages: the conversation shows them
	// with the task card and the task center shows them in the detail.
	assistant.Dispatcher.Notice = func(text string) {
		assistant.ActionError = text
		agentPage.ActionError = text
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

// openConversation switches to the module owning the conversation a task plan
// was started from: the main agent conversation opens in the assistant, a sub
// agent 1:1 conversation opens in the agent module.
func (a *App) openConversation(conversationID string) {
	if a.assistantPage == nil {
		return
	}
	conv, ok := a.assistantPage.Store.Get(conversationID)
	if !ok {
		return
	}
	if conv.AgentID == "" {
		a.assistantPage.SelectConversation(conversationID)
		a.shell.Current = shell.ModuleAssistant
	} else {
		if a.agentPage == nil {
			return
		}
		a.agentPage.SelectAgent(conv.AgentID)
		a.agentPage.SelectConversation(conversationID)
		a.shell.Current = shell.ModuleAgent
	}
	a.invalidateMain()
}

// agentPaths returns the agent workspace root and the task directory. An empty
// root keeps the agent data in memory for this run.
func agentPaths() (agentsDir, taskDir string) {
	dir, err := platform.AgentDir()
	if err != nil {
		log.Printf("app: agent directory: %v; agent data is kept in memory", err)
		dir = ""
	}
	tasks, err := platform.TaskDir()
	if err != nil {
		log.Printf("app: task directory: %v; tasks are kept in memory", err)
		tasks = ""
	}
	return dir, tasks
}

// newAgentPages opens the conversations, the agent workspaces and the tasks in
// the given paths, then selects the most recent main agent conversation. It
// also returns the agent registry for the plugin module.
func newAgentPages(chatter agent.Chatter, agentsDir, taskDir string) (*agent.Page, *agent.AgentPage, *agent.Registry) {
	store := agent.NewStore(agentsDir)
	if err := store.Load(); err != nil {
		log.Printf("app: load conversations: %v", err)
	}
	agents := agent.NewRegistry(agentsDir)
	if err := agents.Load(); err != nil {
		log.Printf("app: load agents: %v", err)
	}
	tasks := task.NewStore(taskDir)
	if err := tasks.Load(); err != nil {
		log.Printf("app: load tasks: %v", err)
	}
	assistant := agent.NewPage(chatter, store, tasks, agents)
	assistant.SelectRecent()
	agentPage := agent.NewAgentPage(chatter, store, tasks, agents, assistant.Runner)
	agentPage.Dispatcher = assistant.Dispatcher
	agentPage.SelectAgent("")
	return assistant, agentPage, agents
}
