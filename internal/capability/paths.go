package capability

import (
	"fmt"
	"path/filepath"
	"strings"
)

// reservedNames are Windows device names that cannot be used as a file or
// directory name.
var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// cleanRelativePath validates a path relative to an app data or grant root.
func cleanRelativePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	if strings.ContainsAny(p, "\\\x00") {
		return "", fmt.Errorf("路径不能包含反斜杠或空字符")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || hasDrivePrefix(p) {
		return "", fmt.Errorf("路径必须是相对路径")
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("路径不能包含空段、. 或 ..")
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return "", fmt.Errorf("路径段不能以点或空格结尾")
		}
		base := segment
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if reservedNames[strings.ToUpper(base)] {
			return "", fmt.Errorf("路径段使用了系统保留名")
		}
	}
	return p, nil
}

// cleanPathOrRoot accepts an empty path as the root of the tree.
func cleanPathOrRoot(p string) (string, error) {
	if p == "" || p == "." {
		return ".", nil
	}
	return cleanRelativePath(p)
}

func hasDrivePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

// joinRoot joins a validated relative path to root.
func joinRoot(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// withinRoot reports whether target stays inside root.
func withinRoot(root, target string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
