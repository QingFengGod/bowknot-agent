package agent

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// SkillProvider supplies the plugin skill section of one agent's system
// prompt. The app injects an adapter over the plugin store; tests use
// stand-ins. A provider that removes a plugin from the enable list reports
// that through its notice, so the conversation can tell the user.
type SkillProvider interface {
	// SkillSection returns the prompt section of the enabled plugins of the
	// agent, plus the notice for plugins that failed injection.
	SkillSection(agentID string) (section, notice string)
}

// pluginsJSON is the on-disk shape of plugins.json.
type pluginsJSON struct {
	Schema  int      `json:"schema"`
	Plugins []string `json:"plugins"`
}

// EnabledPlugins returns the plugin IDs one agent enables, in enable order.
// A missing file means an empty list; a corrupt file is reported and read as
// empty, never rewritten from here.
func (r *Registry) EnabledPlugins(id string) []string {
	if r == nil {
		return nil
	}
	path := r.Workspace(id).Path(PluginsFileName)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.Size() > MaxWorkspaceFileBytes {
		log.Printf("agent: %s exceeds %d bytes", PluginsFileName, MaxWorkspaceFileBytes)
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("agent: read %s: %v", path, err)
		return nil
	}
	var file pluginsJSON
	if err := json.Unmarshal(data, &file); err != nil || file.Schema != AgentSchemaVersion {
		log.Printf("agent: %s is corrupt; the enable list is read as empty", path)
		return nil
	}
	return normalizePluginIDs(file.Plugins)
}

// SetEnabledPlugins replaces the enable list of one agent and writes it to the
// workspace. The list order is preserved: injected skills follow it.
func (r *Registry) SetEnabledPlugins(id string, ids []string) error {
	if r == nil {
		return nil
	}
	file := pluginsJSON{Schema: AgentSchemaVersion, Plugins: normalizePluginIDs(ids)}
	data, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return r.Workspace(id).Save(PluginsFileName, string(data)+"\n")
}

// normalizePluginIDs trims the IDs, drops empties and removes duplicates while
// keeping the order.
func normalizePluginIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
