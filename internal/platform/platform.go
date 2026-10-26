// Package platform 收敛 macOS 与 Windows 之间的平台差异。
//
// 001 只提供平台标识；托盘、全局快捷键、自启动、路径等差异在后续计划中落在这里。
package platform

import "runtime"

// Name 返回当前平台的显示名，用于界面文案。
func Name() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	default:
		return runtime.GOOS
	}
}
