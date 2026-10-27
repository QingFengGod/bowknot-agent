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

// RegistryPath returns the path of the installed app registry in AppsDir.
func RegistryPath() (string, error) {
	dir, err := AppsDir()
	if err != nil {
		return "", err
	}
	return registryPath(dir), nil
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

func registryPath(dir string) string {
	return filepath.Join(dir, "registry.json")
}
