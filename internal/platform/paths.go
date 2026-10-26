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

func dataDir(home string) string {
	return filepath.Join(home, DataDirName)
}

func settingsPath(dir string) string {
	return filepath.Join(dir, "settings.json")
}
