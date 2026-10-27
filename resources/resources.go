// Package resources 提供随包发布的图片资源。
package resources

import _ "embed"

// TrayIcon is the macOS menu bar and Windows notification area icon: a
// 32×32 black-and-transparent PNG of the bowknot, which the platforms tint
// themselves (see mygo.TrayOptions.IconIsTemplate).
//
//go:embed tray.png
var TrayIcon []byte
