package platform

import (
	"fmt"
	"os"
	"path/filepath"
)

// DataDirName is the folder under the user's home directory that holds all
// local data of the app: settings, caches, logs and databases.
const DataDirName = ".bowknot-agent"

// DataDir returns the app's data root, {UserHomeDir}/.bowknot-agent. It does
// not create the directory; use EnsureDataDir before writing into it.
func DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("platform: user home directory: %w", err)
	}
	return dataDir(home), nil
}

// SettingsPath returns the path of settings.json in DataDir.
func SettingsPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return settingsPath(dir), nil
}

// EnsureDataDir returns DataDir, creating it when missing.
func EnsureDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("platform: create data directory: %w", err)
	}
	return dir, nil
}

// AppsDir returns the directory of installed apps in DataDir. It does not
// create the directory.
func AppsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return appsDir(dir), nil
}

// AppDataDir returns the directory of app private data in DataDir. It does not
// create the directory; each app owns the appdata/<id> folder inside it.
func AppDataDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return appDataDir(dir), nil
}

// RegistryPath returns the path of the installed app registry in AppsDir.
func RegistryPath() (string, error) {
	dir, err := AppsDir()
	if err != nil {
		return "", err
	}
	return registryPath(dir), nil
}

// GatewayDir returns the directory holding the AI gateway configuration,
// credentials and usage log in DataDir. It does not create the directory.
func GatewayDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return gatewayDir(dir), nil
}

// AgentDir returns the directory holding assistant conversations in DataDir.
// It does not create the directory.
func AgentDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return agentDir(dir), nil
}

// TaskDir returns the directory holding dispatched task groups in DataDir.
// It does not create the directory.
func TaskDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return taskDir(dir), nil
}

// AgentsDir returns the root of all agent workspaces in DataDir. It does not
// create the directory.
func AgentsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return agentsDir(dir), nil
}

// AgentWorkspaceDir returns the workspace directory agents/<id>. It does not
// create the directory and does not validate id.
func AgentWorkspaceDir(id string) (string, error) {
	dir, err := AgentsDir()
	if err != nil {
		return "", err
	}
	return agentWorkspaceDir(dir, id), nil
}

// GroupsDir returns the root of group chats in DataDir. It does not create
// the directory.
func GroupsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return groupsDir(dir), nil
}

// PluginsDir returns the root of installed plugins in DataDir. It does not
// create the directory.
func PluginsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return pluginsDir(dir), nil
}

func dataDir(home string) string {
	return filepath.Join(home, DataDirName)
}

func settingsPath(dir string) string {
	return filepath.Join(dir, "settings.json")
}

func appsDir(dir string) string {
	return filepath.Join(dir, "apps")
}

func appDataDir(dir string) string {
	return filepath.Join(dir, "appdata")
}

func registryPath(dir string) string {
	return filepath.Join(dir, "registry.json")
}

func gatewayDir(dir string) string {
	return filepath.Join(dir, "gateway")
}

func agentDir(dir string) string {
	return filepath.Join(dir, "agent")
}

func taskDir(dir string) string {
	return filepath.Join(dir, "tasks")
}

func agentsDir(dir string) string {
	return filepath.Join(dir, "agents")
}

func agentWorkspaceDir(agentsRoot, id string) string {
	return filepath.Join(agentsRoot, id)
}

func groupsDir(dir string) string {
	return filepath.Join(dir, "groups")
}

func pluginsDir(dir string) string {
	return filepath.Join(dir, "plugins")
}
